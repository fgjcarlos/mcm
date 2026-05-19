package diagnostics

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

const doctorKeepAliveSeconds = 10

// CheckMosquittoConnection verifies that the configured broker accepts an MQTT connection.
func CheckMosquittoConnection(ctx context.Context, cfg config.MosquittoConfig) error {
	dialer := &net.Dialer{}
	return checkMosquittoConnection(ctx, cfg, dialer.DialContext)
}

type dialContextFunc func(context.Context, string, string) (net.Conn, error)

func checkMosquittoConnection(ctx context.Context, cfg config.MosquittoConfig, dial dialContextFunc) error {
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	conn, err := dial(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("connect to broker: %w", err)
	}
	defer conn.Close()

	if cfg.TLS.Enabled {
		tlsConn, err := wrapTLS(ctx, conn, cfg)
		if err != nil {
			return err
		}
		conn = tlsConn
	}

	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set connection deadline: %w", err)
		}
	} else {
		if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return fmt.Errorf("set connection deadline: %w", err)
		}
	}

	if err := sendMQTTConnect(conn, cfg); err != nil {
		return err
	}

	if err := readMQTTConnAck(conn); err != nil {
		return err
	}

	_, _ = conn.Write([]byte{0xE0, 0x00})
	return nil
}

func wrapTLS(ctx context.Context, conn net.Conn, cfg config.MosquittoConfig) (net.Conn, error) {
	tlsConfig, err := tlsConfigFromMosquittoConfig(cfg)
	if err != nil {
		return nil, err
	}

	tlsConn := tls.Client(conn, tlsConfig)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("TLS handshake with broker: %w", err)
	}

	return tlsConn, nil
}

func tlsConfigFromMosquittoConfig(cfg config.MosquittoConfig) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		ServerName:         cfg.Host,
		InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		MinVersion:         tls.VersionTLS12,
	}

	caPEM, err := os.ReadFile(cfg.TLS.CACertFile)
	if err != nil {
		return nil, fmt.Errorf("read Mosquitto CA certificate %q: %w", cfg.TLS.CACertFile, err)
	}

	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("parse Mosquitto CA certificate %q", cfg.TLS.CACertFile)
	}
	tlsConfig.RootCAs = rootCAs

	clientCert, err := tls.LoadX509KeyPair(cfg.TLS.ClientCertFile, cfg.TLS.ClientKeyFile)
	if err != nil {
		return nil, fmt.Errorf("load Mosquitto client certificate/key: %w", err)
	}
	tlsConfig.Certificates = []tls.Certificate{clientCert}

	return tlsConfig, nil
}

func sendMQTTConnect(w io.Writer, cfg config.MosquittoConfig) error {
	packet, err := mqttConnectPacket(cfg)
	if err != nil {
		return err
	}

	if _, err := w.Write(packet); err != nil {
		return fmt.Errorf("send MQTT CONNECT packet: %w", err)
	}

	return nil
}

func readMQTTConnAck(r io.Reader) error {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return fmt.Errorf("read MQTT CONNACK packet: %w", err)
	}

	if header[0] != 0x20 {
		return fmt.Errorf("unexpected MQTT response packet 0x%02x", header[0])
	}
	if header[1] != 0x02 {
		return fmt.Errorf("unexpected MQTT CONNACK size %d", header[1])
	}
	if header[3] != 0x00 {
		return fmt.Errorf("broker rejected MQTT connection: %s", connAckError(header[3]))
	}

	return nil
}

func mqttConnectPacket(cfg config.MosquittoConfig) ([]byte, error) {
	var payload []byte

	clientID, err := doctorClientID()
	if err != nil {
		return nil, err
	}
	payload = appendStringField(payload, clientID)

	connectFlags := byte(0x02)
	if cfg.Username != "" {
		connectFlags |= 0x80
		payload = appendStringField(payload, cfg.Username)
	}
	if cfg.Password != "" {
		connectFlags |= 0x40
		payload = appendStringField(payload, cfg.Password)
	}

	var variableHeader []byte
	variableHeader = appendStringField(variableHeader, "MQTT")
	variableHeader = append(variableHeader, 0x04, connectFlags)
	variableHeader = binary.BigEndian.AppendUint16(variableHeader, doctorKeepAliveSeconds)

	remainingLength := encodeMQTTRemainingLength(len(variableHeader) + len(payload))
	packet := []byte{0x10}
	packet = append(packet, remainingLength...)
	packet = append(packet, variableHeader...)
	packet = append(packet, payload...)
	return packet, nil
}

func doctorClientID() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate MQTT client id: %w", err)
	}

	return fmt.Sprintf("mcm-doctor-%06d", n.Int64()), nil
}

func appendStringField(dst []byte, value string) []byte {
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(value)))
	dst = append(dst, value...)
	return dst
}

func encodeMQTTRemainingLength(length int) []byte {
	var encoded []byte
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		encoded = append(encoded, digit)
		if length == 0 {
			return encoded
		}
	}
}

func connAckError(code byte) string {
	switch code {
	case 0x01:
		return "unacceptable protocol version"
	case 0x02:
		return "identifier rejected"
	case 0x03:
		return "server unavailable"
	case 0x04:
		return "bad username or password"
	case 0x05:
		return "not authorized"
	default:
		return fmt.Sprintf("unknown return code 0x%02x", code)
	}
}
