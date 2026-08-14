package server

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mqttserver "github.com/mochi-mqtt/server/v2"
	mqttauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"github.com/fgjcarlos/mcm/internal/config"
)

// mochiCloser closes a mochi server exactly once even if the test code also closed it.
type mochiCloser struct {
	server *mqttserver.Server
	once   sync.Once
}

func (c *mochiCloser) Close() {
	c.once.Do(func() {
		_ = c.server.Close()
	})
}

// startMochiBroker spawns an in-process MQTT broker on a free local port. The returned
// closer is registered as t.Cleanup and is also safe to invoke manually mid-test
// (e.g. the reconnect smoke test), guarding against mochi's non-idempotent Close.
func startMochiBroker(t *testing.T) (*mochiCloser, string) {
	t.Helper()
	server, addr := startMochiBrokerAt(t, "")
	closer := &mochiCloser{server: server}
	t.Cleanup(closer.Close)
	return closer, addr
}

func startMochiBrokerAt(t *testing.T, addr string) (*mqttserver.Server, string) {
	t.Helper()

	if addr == "" {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen for free port: %v", err)
		}
		addr = ln.Addr().String()
		_ = ln.Close()
	}

	server := mqttserver.New(&mqttserver.Options{InlineClient: true})
	if err := server.AddHook(new(mqttauth.AllowHook), nil); err != nil {
		t.Fatalf("AddHook: %v", err)
	}
	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "test-tcp-" + addr, Address: addr})); err != nil {
		t.Fatalf("AddListener: %v", err)
	}
	go func() {
		_ = server.Serve()
	}()
	waitForTCPReady(t, addr)
	return server, addr
}

func waitForTCPReady(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("broker did not become ready at %s", addr)
}

func parseHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port := 0
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}

func waitForBrokerEvent(t *testing.T, events <-chan BrokerEvent, accept func(BrokerEvent) bool, timeout time.Duration, what string) BrokerEvent {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("broker event channel closed before %s", what)
			}
			if accept(event) {
				return event
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestBrokerMonitorConnectsToRealMQTTBrokerAndAnnouncesStatus(t *testing.T) {
	_, addr := startMochiBroker(t)
	host, port := parseHostPort(t, addr)

	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	wsUser := seedAdminUser(t, store, "broker-mqtt-status", "broker-mqtt-status-password", false)

	events, unsubscribe := app.brokerEvents.Subscribe(wsUser.ID)
	t.Cleanup(unsubscribe)
	// Drain the buffered initial disconnected status published when the hub was created.
	drainInitialStatus(t, events)

	ctx, cancel := context.WithCancel(context.Background())
	monitorDone := make(chan struct{})
	go func() {
		app.StartBrokerMonitor(ctx, config.MosquittoConfig{Host: host, Port: port})
		close(monitorDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-monitorDone
	})

	event := waitForBrokerEvent(t, events, func(event BrokerEvent) bool {
		return event.Type == "broker_status" && event.Status == "connected"
	}, 3*time.Second, "broker_status connected")
	if !strings.Contains(event.Status, "connected") {
		t.Fatalf("event = %+v, want connected status", event)
	}
}

func TestBrokerMonitorBridgesIncomingTopicMessages(t *testing.T) {
	_, addr := startMochiBroker(t)
	host, port := parseHostPort(t, addr)

	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	wsUser := seedAdminUser(t, store, "broker-mqtt-bridge", "broker-mqtt-bridge-password", false)

	events, unsubscribe := app.brokerEvents.Subscribe(wsUser.ID)
	t.Cleanup(unsubscribe)
	drainInitialStatus(t, events)

	ctx, cancel := context.WithCancel(context.Background())
	monitorDone := make(chan struct{})
	go func() {
		app.StartBrokerMonitor(ctx, config.MosquittoConfig{Host: host, Port: port})
		close(monitorDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-monitorDone
	})

	// Wait until the monitor has subscribed.
	waitForBrokerEvent(t, events, func(event BrokerEvent) bool {
		return event.Type == "broker_status" && event.Status == "connected"
	}, 3*time.Second, "broker_status connected")

	publisher := mqtt.NewClient(mqtt.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("test-publisher"))
	if token := publisher.Connect(); token.WaitTimeout(3*time.Second) && token.Error() != nil {
		t.Fatalf("publisher connect: %v", token.Error())
	}
	t.Cleanup(func() { publisher.Disconnect(100) })

	if token := publisher.Publish("factory/line1/temperature", 0, false, `{"temperature":21.5}`); token.WaitTimeout(3*time.Second) && token.Error() != nil {
		t.Fatalf("publish: %v", token.Error())
	}

	event := waitForBrokerEvent(t, events, func(event BrokerEvent) bool {
		return event.Type == "topic_message" && event.Topic == "factory/line1/temperature"
	}, 3*time.Second, "topic_message")
	if event.PayloadFormat != "json" {
		t.Fatalf("event payload_format = %q, want json", event.PayloadFormat)
	}
}

func TestBrokerMonitorReconnectsAfterBrokerRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("reconnect smoke test is slow")
	}
	closer, addr := startMochiBroker(t)
	host, port := parseHostPort(t, addr)

	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	wsUser := seedAdminUser(t, store, "broker-mqtt-reconnect", "broker-mqtt-reconnect-password", false)

	events, unsubscribe := app.brokerEvents.Subscribe(wsUser.ID)
	t.Cleanup(unsubscribe)
	drainInitialStatus(t, events)

	ctx, cancel := context.WithCancel(context.Background())
	monitorDone := make(chan struct{})
	go func() {
		app.StartBrokerMonitor(ctx, config.MosquittoConfig{Host: host, Port: port})
		close(monitorDone)
	}()
	t.Cleanup(func() {
		cancel()
		<-monitorDone
	})

	waitForBrokerEvent(t, events, func(event BrokerEvent) bool {
		return event.Type == "broker_status" && event.Status == "connected"
	}, 3*time.Second, "initial connected")

	// Tear the broker down. paho will surface a connection lost event.
	closer.Close()
	waitForBrokerEvent(t, events, func(event BrokerEvent) bool {
		return event.Type == "broker_status" && event.Status == "disconnected"
	}, 5*time.Second, "disconnected after broker stop")

	// Restart the broker on the same address so paho's auto-reconnect lands a successful handshake.
	restarted, _ := startMochiBrokerAt(t, addr)
	restartCloser := &mochiCloser{server: restarted}
	t.Cleanup(restartCloser.Close)

	waitForBrokerEvent(t, events, func(event BrokerEvent) bool {
		return event.Type == "broker_status" && event.Status == "connected"
	}, 30*time.Second, "reconnected after broker restart")
}
