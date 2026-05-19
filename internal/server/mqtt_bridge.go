package server

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// MQTTBridge streams broker and topic activity into the websocket state store.
type MQTTBridge struct {
	logger    *slog.Logger
	brokerURL string
	store     *StateStore
	hub       *Hub
}

// NewMQTTBridge creates a bridge bound to a single broker.
func NewMQTTBridge(logger *slog.Logger, brokerURL string, store *StateStore, hub *Hub) *MQTTBridge {
	return &MQTTBridge{
		logger:    logger,
		brokerURL: brokerURL,
		store:     store,
		hub:       hub,
	}
}

// Run connects to the broker and blocks until the context is cancelled.
func (b *MQTTBridge) Run(ctx context.Context) error {
	for {
		clientID := fmt.Sprintf("mcm-dashboard-%d-%d", os.Getpid(), rand.Intn(1_000_000))
		opts := mqtt.NewClientOptions().
			AddBroker(b.brokerURL).
			SetClientID(clientID).
			SetOrderMatters(false).
			SetConnectRetry(true).
			SetAutoReconnect(true).
			SetConnectRetryInterval(3 * time.Second)

		opts.SetOnConnectHandler(func(client mqtt.Client) {
			b.logger.Info("connected to mqtt broker", "broker_url", b.brokerURL)
			b.publishBroker(b.store.SetBrokerConnected(true, ""))

			for _, topic := range []string{"#", "$SYS/#"} {
				token := client.Subscribe(topic, 0, b.handleMessage)
				if ok := token.WaitTimeout(5 * time.Second); !ok || token.Error() != nil {
					err := token.Error()
					if err == nil {
						err = fmt.Errorf("subscribe timeout")
					}
					b.logger.Error("failed to subscribe", "topic", topic, "error", err)
				}
			}
		})

		opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
			b.logger.Warn("mqtt connection lost", "error", err)
			b.publishBroker(b.store.SetBrokerConnected(false, errString(err)))
		})

		client := mqtt.NewClient(opts)
		token := client.Connect()
		if ok := token.WaitTimeout(10 * time.Second); ok && token.Error() == nil {
			<-ctx.Done()
			client.Disconnect(250)
			b.publishBroker(b.store.SetBrokerConnected(false, "server shutdown"))
			return nil
		}

		err := token.Error()
		if err == nil {
			err = fmt.Errorf("mqtt connection timed out")
		}

		b.logger.Warn("mqtt connection failed", "broker_url", b.brokerURL, "error", err)
		b.publishBroker(b.store.SetBrokerConnected(false, err.Error()))

		select {
		case <-ctx.Done():
			b.publishBroker(b.store.SetBrokerConnected(false, "server shutdown"))
			return nil
		case <-time.After(3 * time.Second):
		}
	}
}

func (b *MQTTBridge) handleMessage(_ mqtt.Client, msg mqtt.Message) {
	topic := msg.Topic()
	payload := msg.Payload()

	if broker, ok := b.store.UpdateSystemTopic(topic, payload); ok {
		b.publishBroker(broker)
		return
	}

	if len(topic) >= 5 && topic[:5] == "$SYS/" {
		return
	}

	activity := b.store.AddTopicMessage(topic, payload)
	b.hub.Broadcast(Event{Type: "topic", Topic: &activity})
	b.publishBroker(b.store.Snapshot().Broker)
}

func (b *MQTTBridge) publishBroker(status BrokerStatus) {
	b.hub.Broadcast(Event{Type: "broker", Broker: &status})
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
