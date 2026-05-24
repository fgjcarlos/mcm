package sparkplug

import (
	"math"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
)

// testMetric holds the fields needed to build a wire-format metric message.
type testMetric struct {
	name      string
	alias     uint64
	datatype  uint32
	timestamp uint64
	isNull    bool
	// value fields — at most one should be set
	intValue    *uint64
	floatValue  *float32
	doubleValue *float64
	boolValue   *bool
	stringValue *string
	bytesValue  []byte
}

func encodeTestMetric(m testMetric) []byte {
	var buf []byte

	// field 1: name (string / bytes)
	if m.name != "" {
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendString(buf, m.name)
	}

	// field 2: alias (varint)
	if m.alias != 0 {
		buf = protowire.AppendTag(buf, 2, protowire.VarintType)
		buf = protowire.AppendVarint(buf, m.alias)
	}

	// field 3: timestamp (varint)
	if m.timestamp != 0 {
		buf = protowire.AppendTag(buf, 3, protowire.VarintType)
		buf = protowire.AppendVarint(buf, m.timestamp)
	}

	// field 4: datatype (varint)
	if m.datatype != 0 {
		buf = protowire.AppendTag(buf, 4, protowire.VarintType)
		buf = protowire.AppendVarint(buf, uint64(m.datatype))
	}

	// field 7: is_null (varint bool)
	if m.isNull {
		buf = protowire.AppendTag(buf, 7, protowire.VarintType)
		buf = protowire.AppendVarint(buf, 1)
	}

	// value fields
	if m.intValue != nil {
		// field 8: int_value / long_value (varint)
		buf = protowire.AppendTag(buf, 8, protowire.VarintType)
		buf = protowire.AppendVarint(buf, *m.intValue)
	}
	if m.floatValue != nil {
		// field 10: float_value (fixed32)
		buf = protowire.AppendTag(buf, 10, protowire.Fixed32Type)
		buf = protowire.AppendFixed32(buf, math.Float32bits(*m.floatValue))
	}
	if m.doubleValue != nil {
		// field 11: double_value (fixed64)
		buf = protowire.AppendTag(buf, 11, protowire.Fixed64Type)
		buf = protowire.AppendFixed64(buf, math.Float64bits(*m.doubleValue))
	}
	if m.boolValue != nil {
		// field 12: boolean_value (varint)
		buf = protowire.AppendTag(buf, 12, protowire.VarintType)
		if *m.boolValue {
			buf = protowire.AppendVarint(buf, 1)
		} else {
			buf = protowire.AppendVarint(buf, 0)
		}
	}
	if m.stringValue != nil {
		// field 13: string_value (bytes)
		buf = protowire.AppendTag(buf, 13, protowire.BytesType)
		buf = protowire.AppendString(buf, *m.stringValue)
	}
	if len(m.bytesValue) > 0 {
		// field 14: bytes_value (bytes)
		buf = protowire.AppendTag(buf, 14, protowire.BytesType)
		buf = protowire.AppendBytes(buf, m.bytesValue)
	}

	return buf
}

func buildTestPayload(timestamp uint64, seq uint64, metrics []testMetric) []byte {
	var buf []byte

	// field 2: timestamp (varint)
	buf = protowire.AppendTag(buf, 2, protowire.VarintType)
	buf = protowire.AppendVarint(buf, timestamp)

	// field 4: seq (varint)
	if seq != 0 {
		buf = protowire.AppendTag(buf, 4, protowire.VarintType)
		buf = protowire.AppendVarint(buf, seq)
	}

	// field 1: repeated metric (length-delimited)
	for _, m := range metrics {
		metricBuf := encodeTestMetric(m)
		buf = protowire.AppendTag(buf, 1, protowire.BytesType)
		buf = protowire.AppendBytes(buf, metricBuf)
	}

	return buf
}

func boolPtr(b bool) *bool          { return &b }
func strPtr(s string) *string       { return &s }
func uint64Ptr(v uint64) *uint64    { return &v }
func float32Ptr(v float32) *float32 { return &v }
func float64Ptr(v float64) *float64 { return &v }

func TestDecodePayload(t *testing.T) {
	now := uint64(time.Now().UnixMilli())

	t.Run("valid payload with three diverse metrics", func(t *testing.T) {
		// Sparkplug B datatypes: Int8=1 Int16=2 Int32=3 Int64=4 UInt8=5 UInt16=6
		// UInt32=7 UInt64=8 Float=9 Double=10 Boolean=11 String=12 DateTime=13
		// Text=14 UUID=15 DataSet=16 Bytes=17 Template=18
		metrics := []testMetric{
			{name: "temperature", datatype: 3, intValue: uint64Ptr(42)},  // Int32
			{name: "status", datatype: 11, boolValue: boolPtr(true)},     // Boolean
			{name: "label", datatype: 12, stringValue: strPtr("running")}, // String
		}

		payload := buildTestPayload(now, 5, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Timestamp != int64(now) {
			t.Errorf("timestamp: got %d, want %d", got.Timestamp, int64(now))
		}
		if got.Seq != 5 {
			t.Errorf("seq: got %d, want 5", got.Seq)
		}
		if len(got.Metrics) != 3 {
			t.Fatalf("metrics count: got %d, want 3", len(got.Metrics))
		}
		if got.Truncated {
			t.Error("truncated should be false")
		}
		if got.Metrics[0].Name != "temperature" {
			t.Errorf("metric[0].Name: got %q, want %q", got.Metrics[0].Name, "temperature")
		}
		if got.Metrics[0].Datatype != "Int32" {
			t.Errorf("metric[0].Datatype: got %q, want %q", got.Metrics[0].Datatype, "Int32")
		}
		if got.Metrics[1].Name != "status" {
			t.Errorf("metric[1].Name: got %q, want %q", got.Metrics[1].Name, "status")
		}
		if got.Metrics[1].Datatype != "Boolean" {
			t.Errorf("metric[1].Datatype: got %q, want %q", got.Metrics[1].Datatype, "Boolean")
		}
		if got.Metrics[2].Name != "label" {
			t.Errorf("metric[2].Name: got %q, want %q", got.Metrics[2].Name, "label")
		}
		if got.Metrics[2].Datatype != "String" {
			t.Errorf("metric[2].Datatype: got %q, want %q", got.Metrics[2].Datatype, "String")
		}
	})

	t.Run("empty data returns error", func(t *testing.T) {
		_, err := DecodePayload([]byte{}, 50)
		if err == nil {
			t.Error("expected error for empty data")
		}
	})

	t.Run("nil data returns error", func(t *testing.T) {
		_, err := DecodePayload(nil, 50)
		if err == nil {
			t.Error("expected error for nil data")
		}
	})

	t.Run("corrupt bytes returns error", func(t *testing.T) {
		// Tag says field 1 (metric), bytes type, length 0xff...01 (large varint) — truncated body
		corrupt := []byte{0x0a, 0xff, 0xff, 0xff, 0xff, 0x01}
		_, err := DecodePayload(corrupt, 50)
		if err == nil {
			t.Error("expected error for corrupt/truncated data")
		}
	})

	t.Run("oversized payload returns error", func(t *testing.T) {
		big := make([]byte, 1024*1024+1)
		_, err := DecodePayload(big, 50)
		if err == nil {
			t.Error("expected error for oversized payload")
		}
	})

	t.Run("truncation at maxMetrics", func(t *testing.T) {
		var metrics []testMetric
		for i := 0; i < 10; i++ {
			metrics = append(metrics, testMetric{
				name:      "m",
				datatype:  11,
				boolValue: boolPtr(true),
			})
		}
		payload := buildTestPayload(now, 1, metrics)
		got, err := DecodePayload(payload, 5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Metrics) != 5 {
			t.Errorf("metrics count: got %d, want 5", len(got.Metrics))
		}
		if !got.Truncated {
			t.Error("expected Truncated=true")
		}
	})

	t.Run("metric with unknown datatype gets Unknown", func(t *testing.T) {
		metrics := []testMetric{
			{name: "x", datatype: 255},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Metrics) != 1 {
			t.Fatalf("expected 1 metric, got %d", len(got.Metrics))
		}
		if got.Metrics[0].Datatype != "Unknown" {
			t.Errorf("datatype: got %q, want %q", got.Metrics[0].Datatype, "Unknown")
		}
	})

	t.Run("boolean true metric decodes to Go bool true", func(t *testing.T) {
		metrics := []testMetric{
			{name: "flag", datatype: 11, boolValue: boolPtr(true)},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got.Metrics) == 0 {
			t.Fatal("expected metrics")
		}
		v, ok := got.Metrics[0].Value.(bool)
		if !ok {
			t.Fatalf("value type: got %T, want bool", got.Metrics[0].Value)
		}
		if !v {
			t.Error("expected true")
		}
	})

	t.Run("boolean false metric decodes to Go bool false", func(t *testing.T) {
		metrics := []testMetric{
			{name: "flag", datatype: 11, boolValue: boolPtr(false)},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, ok := got.Metrics[0].Value.(bool)
		if !ok {
			t.Fatalf("value type: got %T, want bool", got.Metrics[0].Value)
		}
		if v {
			t.Error("expected false")
		}
	})

	t.Run("string metric decodes to Go string", func(t *testing.T) {
		metrics := []testMetric{
			{name: "label", datatype: 12, stringValue: strPtr("hello")},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, ok := got.Metrics[0].Value.(string)
		if !ok {
			t.Fatalf("value type: got %T, want string", got.Metrics[0].Value)
		}
		if v != "hello" {
			t.Errorf("value: got %q, want %q", v, "hello")
		}
	})

	t.Run("integer metric decodes to uint64", func(t *testing.T) {
		metrics := []testMetric{
			{name: "count", datatype: 7, intValue: uint64Ptr(99)}, // UInt32
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, ok := got.Metrics[0].Value.(uint64)
		if !ok {
			t.Fatalf("value type: got %T, want uint64", got.Metrics[0].Value)
		}
		if v != 99 {
			t.Errorf("value: got %d, want 99", v)
		}
	})

	t.Run("is_null metric has IsNull true and nil value", func(t *testing.T) {
		metrics := []testMetric{
			{name: "nullmetric", datatype: 3, isNull: true},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !got.Metrics[0].IsNull {
			t.Error("expected IsNull=true")
		}
		if got.Metrics[0].Value != nil {
			t.Errorf("expected nil value for null metric, got %v", got.Metrics[0].Value)
		}
	})

	t.Run("metric alias is decoded", func(t *testing.T) {
		metrics := []testMetric{
			{name: "x", alias: 42, datatype: 11, boolValue: boolPtr(false)},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Metrics[0].Alias != 42 {
			t.Errorf("alias: got %d, want 42", got.Metrics[0].Alias)
		}
	})

	t.Run("payload without seq field defaults seq to zero", func(t *testing.T) {
		payload := buildTestPayload(now, 0, nil)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Seq != 0 {
			t.Errorf("seq: got %d, want 0", got.Seq)
		}
	})

	t.Run("float metric decodes to float32", func(t *testing.T) {
		metrics := []testMetric{
			{name: "temp", datatype: 9, floatValue: float32Ptr(3.14)}, // Float
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, ok := got.Metrics[0].Value.(float32)
		if !ok {
			t.Fatalf("value type: got %T, want float32", got.Metrics[0].Value)
		}
		if v < 3.13 || v > 3.15 {
			t.Errorf("float value out of expected range: %f", v)
		}
	})

	t.Run("double metric decodes to float64", func(t *testing.T) {
		metrics := []testMetric{
			{name: "precise", datatype: 10, doubleValue: float64Ptr(2.718281828)}, // Double
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, ok := got.Metrics[0].Value.(float64)
		if !ok {
			t.Fatalf("value type: got %T, want float64", got.Metrics[0].Value)
		}
		if v < 2.71 || v > 2.72 {
			t.Errorf("double value out of expected range: %f", v)
		}
	})

	t.Run("datatype name mapping coverage", func(t *testing.T) {
		cases := []struct {
			dt   uint32
			want string
		}{
			{1, "Int8"},
			{2, "Int16"},
			{3, "Int32"},
			{4, "Int64"},
			{5, "UInt8"},
			{6, "UInt16"},
			{7, "UInt32"},
			{8, "UInt64"},
			{9, "Float"},
			{10, "Double"},
			{11, "Boolean"},
			{12, "String"},
			{13, "DateTime"},
			{14, "Text"},
			{15, "UUID"},
			{16, "DataSet"},
			{17, "Bytes"},
			{18, "Template"},
			{99, "Unknown"},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.want, func(t *testing.T) {
				payload := buildTestPayload(now, 0, []testMetric{
					{name: "x", datatype: tc.dt},
				})
				got, err := DecodePayload(payload, 50)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(got.Metrics) == 0 {
					t.Fatal("no metrics decoded")
				}
				if got.Metrics[0].Datatype != tc.want {
					t.Errorf("datatype %d: got %q, want %q", tc.dt, got.Metrics[0].Datatype, tc.want)
				}
			})
		}
	})

	t.Run("long string value decoded correctly", func(t *testing.T) {
		long := strings.Repeat("x", 500)
		metrics := []testMetric{
			{name: "longval", datatype: 12, stringValue: &long},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		v, ok := got.Metrics[0].Value.(string)
		if !ok {
			t.Fatalf("value type: got %T, want string", got.Metrics[0].Value)
		}
		if len(v) != 500 {
			t.Errorf("string length: got %d, want 500", len(v))
		}
	})

	t.Run("metric timestamp is decoded", func(t *testing.T) {
		ts := uint64(1700000000000)
		metrics := []testMetric{
			{name: "x", datatype: 11, timestamp: ts, boolValue: boolPtr(true)},
		}
		payload := buildTestPayload(now, 0, metrics)
		got, err := DecodePayload(payload, 50)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Metrics[0].Timestamp != int64(ts) {
			t.Errorf("metric timestamp: got %d, want %d", got.Metrics[0].Timestamp, int64(ts))
		}
	})
}
