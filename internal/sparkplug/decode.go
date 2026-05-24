package sparkplug

import (
	"errors"
	"math"

	"google.golang.org/protobuf/encoding/protowire"
)

const maxPayloadSize = 1024 * 1024 // 1 MB

// DecodedPayload holds the decoded content of a Sparkplug B protobuf payload.
type DecodedPayload struct {
	Timestamp int64           `json:"timestamp"`
	Seq       uint64          `json:"seq"`
	Metrics   []DecodedMetric `json:"metrics"`
	Truncated bool            `json:"truncated"`
}

// DecodedMetric holds the decoded fields from a single Sparkplug B Metric message.
type DecodedMetric struct {
	Name      string `json:"name"`
	Alias     uint64 `json:"alias,omitempty"`
	Datatype  string `json:"datatype"`
	Value     any    `json:"value"`
	Timestamp int64  `json:"timestamp,omitempty"`
	IsNull    bool   `json:"is_null,omitempty"`
}

// datatypeNames maps Sparkplug B datatype IDs to human-readable strings.
// Reference: Sparkplug B specification §5.1.
var datatypeNames = map[uint32]string{
	1:  "Int8",
	2:  "Int16",
	3:  "Int32",
	4:  "Int64",
	5:  "UInt8",
	6:  "UInt16",
	7:  "UInt32",
	8:  "UInt64",
	9:  "Float",
	10: "Double",
	11: "Boolean",
	12: "String",
	13: "DateTime",
	14: "Text",
	15: "UUID",
	16: "DataSet",
	17: "Bytes",
	18: "Template",
}

// datatypeName returns the human-readable name for a Sparkplug B datatype ID.
func datatypeName(dt uint32) string {
	if name, ok := datatypeNames[dt]; ok {
		return name
	}
	return "Unknown"
}

// DecodePayload parses a Sparkplug B protobuf payload using raw wire-format
// decoding (no generated protobuf types required). It extracts the top-level
// Payload fields (timestamp, seq) and up to maxMetrics Metric sub-messages.
//
// Returns an error for empty or oversized payloads and for malformed wire data.
// When the number of encoded metrics exceeds maxMetrics, the Truncated field is
// set to true and only the first maxMetrics metrics are returned.
func DecodePayload(data []byte, maxMetrics int) (*DecodedPayload, error) {
	if len(data) == 0 {
		return nil, errors.New("sparkplug: empty payload")
	}
	if len(data) > maxPayloadSize {
		return nil, errors.New("sparkplug: payload exceeds 1 MB limit")
	}
	if maxMetrics <= 0 {
		maxMetrics = 50
	}

	result := &DecodedPayload{}
	var rawMetrics [][]byte

	// Parse the top-level Payload message wire format.
	// Sparkplug B Payload proto3 field numbers:
	//   1  = repeated Metric metrics
	//   2  = uint64 timestamp
	//   3  = repeated bytes body (ignored)
	//   4  = uint64 seq
	b := data
	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil, errors.New("sparkplug: corrupt tag in payload")
		}
		b = b[n:]

		switch {
		case num == 2 && typ == protowire.VarintType:
			// timestamp
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil, errors.New("sparkplug: corrupt timestamp field")
			}
			result.Timestamp = int64(v)
			b = b[n:]

		case num == 4 && typ == protowire.VarintType:
			// seq
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return nil, errors.New("sparkplug: corrupt seq field")
			}
			result.Seq = v
			b = b[n:]

		case num == 1 && typ == protowire.BytesType:
			// repeated Metric
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return nil, errors.New("sparkplug: corrupt metric field")
			}
			// Keep a copy of the bytes for later parsing
			rawMetrics = append(rawMetrics, append([]byte(nil), v...))
			b = b[n:]

		default:
			// Skip unknown or unrequested fields
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return nil, errors.New("sparkplug: corrupt unknown field")
			}
			b = b[n:]
		}
	}

	// Decode metrics, applying the maxMetrics cap
	total := len(rawMetrics)
	if total > maxMetrics {
		rawMetrics = rawMetrics[:maxMetrics]
		result.Truncated = true
	}

	result.Metrics = make([]DecodedMetric, 0, len(rawMetrics))
	for _, raw := range rawMetrics {
		m, err := decodeMetric(raw)
		if err != nil {
			// Skip metrics that cannot be decoded; log is not available here so
			// we silently continue — callers should not fail on partial data.
			continue
		}
		result.Metrics = append(result.Metrics, m)
	}

	// If all metrics were skipped due to errors and we had valid metrics, that
	// is acceptable — return what we have. Only fail on structural errors above.
	_ = total
	return result, nil
}

// decodeMetric parses a single Sparkplug B Metric wire message.
// Sparkplug B Metric proto3 field numbers (simplified subset):
//
//	1  = string name
//	2  = uint64 alias
//	3  = uint64 timestamp
//	4  = uint32 datatype
//	7  = bool is_null
//	8  = uint64 int_value  (Int8 / Int16 / Int32 / Int64 / UInt8 / UInt16 / UInt32 / UInt64)
//	10 = fixed32 float_value   (Float)
//	11 = fixed64 double_value  (Double)
//	12 = uint64 boolean_value  (Boolean)
//	13 = bytes string_value    (String / Text / UUID / DateTime)
//	14 = bytes bytes_value     (Bytes / DataSet / Template)
func decodeMetric(data []byte) (DecodedMetric, error) {
	var m DecodedMetric
	b := data

	for len(b) > 0 {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return DecodedMetric{}, errors.New("sparkplug: corrupt tag in metric")
		}
		b = b[n:]

		switch {
		case num == 1 && typ == protowire.BytesType:
			// name
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric name")
			}
			m.Name = string(v)
			b = b[n:]

		case num == 2 && typ == protowire.VarintType:
			// alias
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric alias")
			}
			m.Alias = v
			b = b[n:]

		case num == 3 && typ == protowire.VarintType:
			// metric timestamp
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric timestamp")
			}
			m.Timestamp = int64(v)
			b = b[n:]

		case num == 4 && typ == protowire.VarintType:
			// datatype
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric datatype")
			}
			m.Datatype = datatypeName(uint32(v))
			b = b[n:]

		case num == 7 && typ == protowire.VarintType:
			// is_null
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric is_null")
			}
			m.IsNull = v != 0
			b = b[n:]

		case num == 8 && typ == protowire.VarintType:
			// int_value / long_value — covers Int8..UInt64
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric int_value")
			}
			if !m.IsNull {
				m.Value = v
			}
			b = b[n:]

		case num == 10 && typ == protowire.Fixed32Type:
			// float_value
			v, n := protowire.ConsumeFixed32(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric float_value")
			}
			if !m.IsNull {
				m.Value = math.Float32frombits(v)
			}
			b = b[n:]

		case num == 11 && typ == protowire.Fixed64Type:
			// double_value
			v, n := protowire.ConsumeFixed64(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric double_value")
			}
			if !m.IsNull {
				m.Value = math.Float64frombits(v)
			}
			b = b[n:]

		case num == 12 && typ == protowire.VarintType:
			// boolean_value
			v, n := protowire.ConsumeVarint(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric boolean_value")
			}
			if !m.IsNull {
				m.Value = v != 0
			}
			b = b[n:]

		case num == 13 && typ == protowire.BytesType:
			// string_value
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric string_value")
			}
			if !m.IsNull {
				m.Value = string(v)
			}
			b = b[n:]

		case num == 14 && typ == protowire.BytesType:
			// bytes_value (Bytes / DataSet / Template payloads)
			v, n := protowire.ConsumeBytes(b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt metric bytes_value")
			}
			if !m.IsNull {
				dst := make([]byte, len(v))
				copy(dst, v)
				m.Value = dst
			}
			b = b[n:]

		default:
			// Skip unknown fields
			n := protowire.ConsumeFieldValue(num, typ, b)
			if n < 0 {
				return DecodedMetric{}, errors.New("sparkplug: corrupt field in metric")
			}
			b = b[n:]
		}
	}

	// Null metrics must not carry a value
	if m.IsNull {
		m.Value = nil
	}

	return m, nil
}
