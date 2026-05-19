package acl

import (
	"fmt"
	"strings"
)

// Permission describes the allowed MQTT action for an ACL rule.
type Permission string

const (
	PermissionRead      Permission = "read"
	PermissionWrite     Permission = "write"
	PermissionReadWrite Permission = "readwrite"
)

var validPermissions = map[Permission]struct{}{
	PermissionRead:      {},
	PermissionWrite:     {},
	PermissionReadWrite: {},
}

// Rule represents one ACL entry managed by MCM.
type Rule struct {
	ID          string     `json:"id"`
	Principal   string     `json:"principal"`
	TopicFilter string     `json:"topic_filter"`
	Permission  Permission `json:"permission"`
	Description string     `json:"description,omitempty"`
}

// ValidationError holds all ACL validation failures.
type ValidationError struct {
	Problems []string `json:"details"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("acl validation failed: %s", strings.Join(e.Problems, "; "))
}

// ValidateRule validates a rule before it is persisted.
func ValidateRule(rule Rule) error {
	var problems []string

	if strings.TrimSpace(rule.Principal) == "" {
		problems = append(problems, "principal is required")
	}
	if strings.TrimSpace(rule.TopicFilter) == "" {
		problems = append(problems, "topic_filter is required")
	} else if err := ValidateTopicFilter(rule.TopicFilter); err != nil {
		problems = append(problems, err.Error())
	}
	if _, ok := validPermissions[rule.Permission]; !ok {
		problems = append(problems, `permission must be one of: "read", "write", "readwrite"`)
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}

	return nil
}

// ValidateTopicFilter validates an MQTT topic filter with MQTT wildcard rules.
func ValidateTopicFilter(filter string) error {
	if filter == "" {
		return fmt.Errorf("topic_filter is required")
	}
	if strings.ContainsRune(filter, '\x00') {
		return fmt.Errorf("topic_filter must not contain NUL")
	}

	levels := strings.Split(filter, "/")
	for idx, level := range levels {
		if strings.Contains(level, "#") {
			if level != "#" {
				return fmt.Errorf(`topic_filter %q is invalid: "#" must occupy an entire topic level`, filter)
			}
			if idx != len(levels)-1 {
				return fmt.Errorf(`topic_filter %q is invalid: "#" must only appear in the final topic level`, filter)
			}
		}

		if strings.Contains(level, "+") && level != "+" {
			return fmt.Errorf(`topic_filter %q is invalid: "+" must occupy an entire topic level`, filter)
		}
	}

	return nil
}

// MosquittoACL returns the Mosquitto ACL line for a rule.
func (r Rule) MosquittoACL() string {
	return fmt.Sprintf("topic %s %s", r.Permission, r.TopicFilter)
}
