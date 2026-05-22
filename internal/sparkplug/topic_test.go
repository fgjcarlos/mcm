package sparkplug

import "testing"

func TestParseTopicValidSparkplugTopics(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		want  Metadata
	}{
		{
			name:  "NBIRTH node birth",
			topic: "spBv1.0/PlantA/NBIRTH/Line1",
			want:  Metadata{Namespace: Namespace, GroupID: "PlantA", MessageType: "NBIRTH", EdgeNodeID: "Line1"},
		},
		{
			name:  "DBIRTH device birth",
			topic: "spBv1.0/PlantA/DBIRTH/Line1/Motor7",
			want:  Metadata{Namespace: Namespace, GroupID: "PlantA", MessageType: "DBIRTH", EdgeNodeID: "Line1", DeviceID: "Motor7"},
		},
		{
			name:  "NDATA node data",
			topic: "spBv1.0/PlantA/NDATA/Line1",
			want:  Metadata{Namespace: Namespace, GroupID: "PlantA", MessageType: "NDATA", EdgeNodeID: "Line1"},
		},
		{
			name:  "DDATA device data",
			topic: "spBv1.0/PlantA/DDATA/Line1/Motor7",
			want:  Metadata{Namespace: Namespace, GroupID: "PlantA", MessageType: "DDATA", EdgeNodeID: "Line1", DeviceID: "Motor7"},
		},
		{
			name:  "NCMD node command",
			topic: "spBv1.0/PlantA/NCMD/Line1",
			want:  Metadata{Namespace: Namespace, GroupID: "PlantA", MessageType: "NCMD", EdgeNodeID: "Line1"},
		},
		{
			name:  "DCMD device command",
			topic: "spBv1.0/PlantA/DCMD/Line1/Motor7",
			want:  Metadata{Namespace: Namespace, GroupID: "PlantA", MessageType: "DCMD", EdgeNodeID: "Line1", DeviceID: "Motor7"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ParseTopic(test.topic)
			if !ok {
				t.Fatalf("ParseTopic(%q) ok=false, want true", test.topic)
			}
			if got != test.want {
				t.Fatalf("ParseTopic(%q) = %+v, want %+v", test.topic, got, test.want)
			}
		})
	}
}

func TestParseTopicInvalidTopics(t *testing.T) {
	tests := []string{
		"factory/line1/temperature",
		"spBv1.0/PlantA/NBIRTH",
		"spBv1.0/PlantA/NBIRTH/Line1/Device1",
		"spBv1.0/PlantA/DBIRTH/Line1",
		"spBv1.0/PlantA/UNKNOWN/Line1",
		"spBv1.0//NBIRTH/Line1",
		"spBv1.0/PlantA/NDATA/",
		"spBv1.0/PlantA/NDATA/Line1/Device1/extra",
		"spBv1.0/PlantA/NDATA/+",
		"spBv1.0/PlantA/DDATA/Line1/#",
		"spBv2.0/PlantA/NBIRTH/Line1",
	}

	for _, topic := range tests {
		t.Run(topic, func(t *testing.T) {
			if got, ok := ParseTopic(topic); ok {
				t.Fatalf("ParseTopic(%q) = %+v, true; want false", topic, got)
			}
			if got := ClassifyTopic(topic); got != nil {
				t.Fatalf("ClassifyTopic(%q) = %+v, want nil", topic, got)
			}
		})
	}
}
