package dynsec

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// ErrNotConnected is returned when the adapter is used before it is
// connected.
var ErrNotConnected = errors.New("dynamic security control channel is not connected")

// ErrResponseMismatch is returned when the plugin answers with something
// other than what was asked for.
//
// The plugin answers every command of a request with one response. When
// the answer does not line up, MCM cannot tell what the broker applied,
// so the change is refused instead of assumed.
var ErrResponseMismatch = errors.New("dynamic security plugin returned an unexpected response")

// ErrConnectionLost is returned when the control channel drops while a
// request is in flight.
//
// The plugin disconnects the client it just changed, so a change that
// touches the account MCM is connected as never reaches MCM: the reply
// is published after the disconnect. The outcome of such a change is
// unknown, so the caller reads the database back and compares it with
// the requested state instead of assuming the change landed.
var ErrConnectionLost = errors.New("the dynamic security control channel dropped")

// Options configures the adapter.
type Options struct {
	// Address is the "host:port" of the broker.
	Address string
	// Username authenticates the management account.
	Username string
	// Password authenticates the management account.
	Password string
	// ClientID is the MQTT client id of the management connection.
	ClientID string
	// TLS enables an encrypted connection when it is not nil.
	TLS *tls.Config
	// Timeout bounds a single request. Zero means ten seconds.
	Timeout time.Duration
	// Logger receives the operational messages. It may be nil.
	Logger *slog.Logger
}

func (o Options) timeout() time.Duration {
	if o.Timeout > 0 {
		return o.Timeout
	}
	return 10 * time.Second
}

// Transport is the contract of the control channel of the plugin.
//
// The deploy service depends on this interface rather than on the MQTT
// client, so the tests can drive it without a broker.
type Transport interface {
	// Do sends the commands and returns the answer of the plugin. Every
	// command must be answered exactly once.
	Do(ctx context.Context, commands ...Command) ([]CommandResponse, error)
}

// Adapter speaks the control protocol of the dynamic security plugin
// over MQTT.
//
// The adapter keeps one connection open and subscribes to the response
// topic of the plugin. Every request carries a correlation value per
// command, so the answer of the plugin can be matched with the request
// it belongs to even when several requests are in flight.
//
// The plugin disconnects the client it just changed, which happens
// whenever the management account itself is changed. The adapter opens a
// fresh connection when that happens instead of waiting for the client
// library to reconnect.
type Adapter struct {
	opts Options

	mu sync.Mutex
	// client is the open connection, nil when there is none.
	client mqtt.Client
	// inbox receives the answers of the plugin.
	inbox chan Response
	// sequence numbers the correlation values of the requests.
	sequence atomic.Uint64
	// lost marks a connection that dropped while a request was in
	// flight.
	lost atomic.Bool
}

// NewAdapter creates an adapter for the given options.
func NewAdapter(opts Options) *Adapter {
	if opts.ClientID == "" {
		opts.ClientID = "mcm-dynsec"
	}
	return &Adapter{
		opts:  opts,
		inbox: make(chan Response, 64),
	}
}

// Connect opens the connection to the broker and subscribes to the
// response topic of the plugin.
//
// The subscription is refused when the management account is not allowed
// to reach the control channel. That refusal is the answer to the
// question whether the channel is protected, so it is reported as an
// error instead of being ignored.
func (a *Adapter) Connect(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.connectLocked(ctx)
}

// connectLocked opens the connection. The caller holds the lock.
func (a *Adapter) connectLocked(ctx context.Context) error {
	if a.client != nil && a.client.IsConnectionOpen() {
		return nil
	}
	if a.client != nil {
		a.client.Disconnect(100)
		a.client = nil
	}

	scheme := "tcp"
	if a.opts.TLS != nil {
		scheme = "ssl"
	}
	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("%s://%s", scheme, a.opts.Address)).
		SetClientID(a.opts.ClientID).
		SetCleanSession(true).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetConnectTimeout(a.opts.timeout()).
		SetAutoReconnect(false).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectRetry(false)
	if a.opts.TLS != nil {
		opts.SetTLSConfig(a.opts.TLS)
	}
	if a.opts.Username != "" {
		opts.SetUsername(a.opts.Username)
		opts.SetPassword(a.opts.Password)
	}
	opts.SetDefaultPublishHandler(func(_ mqtt.Client, msg mqtt.Message) {
		a.handleMessage(msg)
	})
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		// The plugin disconnects the client it just changed. A request
		// that is in flight then never sees its answer.
		a.lost.Store(true)
		a.log("dynsec_connection_lost", "error", err.Error())
	})

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(a.opts.timeout()) {
		return fmt.Errorf("connect to the broker at %s: timeout", a.opts.Address)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("connect to the broker at %s: %w", a.opts.Address, err)
	}

	subscribeToken := client.Subscribe(ResponseTopic, 1, nil)
	if !subscribeToken.WaitTimeout(a.opts.timeout()) {
		client.Disconnect(100)
		return fmt.Errorf("subscribe to %s: timeout", ResponseTopic)
	}
	if err := subscribeToken.Error(); err != nil {
		client.Disconnect(100)
		return fmt.Errorf("subscribe to %s: %w", ResponseTopic, err)
	}
	// The broker refuses a subscription with the return code 0x80, which
	// the client library reports per topic and not as an error of the
	// call. A refused subscription means the management account may not
	// reach the management channel, which is exactly what the operator
	// has to know.
	if sub, ok := subscribeToken.(*mqtt.SubscribeToken); ok {
		if code, found := sub.Result()[ResponseTopic]; found && code == 0x80 {
			client.Disconnect(100)
			return fmt.Errorf("subscribe to %s: the management account may not reach the management channel", ResponseTopic)
		}
	}

	a.client = client
	a.lost.Store(false)
	return nil
}

// ResponseTopic is the topic the plugin publishes its answers on.
const ResponseTopic = ControlTopic + "/response"

// handleMessage routes an answer of the plugin to the waiting caller.
func (a *Adapter) handleMessage(msg mqtt.Message) {
	var response Response
	if err := json.Unmarshal(msg.Payload(), &response); err != nil {
		a.log("dynsec_response_invalid", "error", err.Error())
		return
	}
	select {
	case a.inbox <- response:
	default:
		// The inbox is full: the answer cannot be matched with a
		// request any more. The waiting caller times out and reports
		// the mismatch instead of acting on a stale answer.
		a.log("dynsec_inbox_full", "responses", len(response.Responses))
	}
}

// Do sends the commands to the plugin and returns its answer.
//
// Every command must be answered exactly once, and the answer must carry
// the correlation value of the command it belongs to. An answer that
// does not line up is refused: MCM never assumes that a change it could
// not verify was applied.
func (a *Adapter) Do(ctx context.Context, commands ...Command) ([]CommandResponse, error) {
	if len(commands) == 0 {
		return nil, nil
	}
	a.mu.Lock()
	if a.lost.Load() {
		// The plugin disconnects the client it just changed. The old
		// connection is of no use any more, so a fresh one is opened.
		if a.client != nil {
			a.client.Disconnect(100)
			a.client = nil
		}
		a.lost.Store(false)
	}
	err := a.connectLocked(ctx)
	a.mu.Unlock()
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	client := a.client
	a.mu.Unlock()
	if client == nil {
		return nil, ErrNotConnected
	}

	prefix := fmt.Sprintf("mcm-%d-", a.sequence.Add(1))
	expected := make(map[string]string, len(commands))
	sent := make([]Command, 0, len(commands))
	for i := range commands {
		correlation := fmt.Sprintf("%s%d", prefix, i)
		commands[i].CorrelationData = correlation
		expected[correlation] = commands[i].Name
		sent = append(sent, commands[i])
	}

	body, err := json.Marshal(Request{Commands: sent})
	if err != nil {
		return nil, fmt.Errorf("encode dynamic security request: %w", err)
	}
	token := client.Publish(ControlTopic, 1, false, body)
	if !token.WaitTimeout(a.opts.timeout()) {
		return nil, fmt.Errorf("publish to %s: timeout", ControlTopic)
	}
	if err := token.Error(); err != nil {
		// The plugin may have disconnected the client while the
		// request was on its way. The answer of such a request never
		// arrives, so the caller has to read the state back.
		time.Sleep(100 * time.Millisecond)
		if a.lost.Load() || !client.IsConnectionOpen() {
			return nil, fmt.Errorf("%w: %s", ErrConnectionLost, err.Error())
		}
		return nil, fmt.Errorf("publish to %s: %w", ControlTopic, err)
	}

	deadline := time.NewTimer(a.opts.timeout())
	defer deadline.Stop()

	collected := make([]CommandResponse, 0, len(expected))
	seen := map[string]bool{}
	for len(collected) < len(expected) {
		if a.lost.Load() {
			return nil, fmt.Errorf("%w: the plugin disconnected the client that sent the request", ErrConnectionLost)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("%w: got %d of %d answers", ErrResponseMismatch, len(collected), len(expected))
		case response := <-a.inbox:
			for _, entry := range response.Responses {
				name, ok := expected[entry.CorrelationData]
				if !ok {
					// An answer of another request, or one
					// without a correlation value. It is
					// not part of this request.
					continue
				}
				if seen[entry.CorrelationData] {
					return nil, fmt.Errorf("%w: %q was answered twice", ErrResponseMismatch, name)
				}
				seen[entry.CorrelationData] = true
				collected = append(collected, entry)
			}
		}
	}
	return collected, nil
}

// Close ends the connection to the broker.
func (a *Adapter) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil {
		return
	}
	a.client.Disconnect(100)
	a.client = nil
}

func (a *Adapter) log(msg string, args ...any) {
	if a.opts.Logger == nil {
		return
	}
	a.opts.Logger.Info(msg, args...)
}
