package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

// ErrBrokerEffectiveFilesMismatch is returned when the broker's effective
// password_file / acl_file do not point at the files MCM writes.
//
// The deploy service can only control the access control the broker
// enforces when the broker reads exactly the files MCM renders. A broker
// that reads a different password file (the legacy `passwords` path, a
// file outside the shared volume) keeps enforcing the previous policy
// while MCM reports every apply as successful, so the mismatch is
// reported at startup instead of being discovered by an operator
// (issue #294, acceptance criterion "validar archivos efectivos del
// broker").
var ErrBrokerEffectiveFilesMismatch = errors.New("broker effective files do not match the configured deploy paths")

// EffectiveFilesError describes one mismatch between the broker's
// effective configuration and the paths MCM is configured to write.
type EffectiveFilesError struct {
	// Directive is the broker directive that carries the path
	// ("password_file" or "acl_file").
	Directive string
	// BrokerPath is the path the broker reads, in the broker's own
	// namespace.
	BrokerPath string
	// ExpectedPath is the path MCM writes, in the MCM namespace.
	ExpectedPath string
	// Reason explains the mismatch in one sentence.
	Reason string
}

func (e *EffectiveFilesError) Error() string {
	if e.BrokerPath == "" {
		return fmt.Sprintf("%s: %s (%s)", ErrBrokerEffectiveFilesMismatch, e.Reason, e.Directive)
	}
	return fmt.Sprintf("%s: %s (%s): the broker reads %q but MCM writes %q",
		ErrBrokerEffectiveFilesMismatch, e.Reason, e.Directive, e.BrokerPath, e.ExpectedPath)
}

// Unwrap exposes ErrBrokerEffectiveFilesMismatch to errors.Is.
func (e *EffectiveFilesError) Unwrap() error { return ErrBrokerEffectiveFilesMismatch }

// EffectiveFilesOptions describes the broker's effective files and the
// paths MCM writes.
type EffectiveFilesOptions struct {
	// BrokerConfigPath is the path of the broker's mosquitto.conf as
	// the MCM process sees it. In a containerised deployment this is
	// the shared configuration volume mounted under a different path
	// than the broker's own mount.
	BrokerConfigPath string
	// BrokerConfigDir is the broker-side path of the directory that
	// holds the broker configuration (the broker's own mount point of
	// the shared volume). It is used to translate a broker path into
	// the MCM namespace. Empty means both processes see the same path.
	BrokerConfigDir string
	// ACLPath is the ACL file MCM writes.
	ACLPath string
	// PasswdPath is the password file MCM writes.
	PasswdPath string
}

// CheckBrokerEffectiveFiles compares the password_file and acl_file the
// broker actually reads with the files MCM is configured to write.
//
// The check is deliberately about the broker's EFFECTIVE configuration:
// it reads the same mosquitto.conf the broker loaded, so a stale or
// mismatching directive is detected before any deploy is applied.
//
// It returns an *EffectiveFilesError when a directive is missing or
// points at another file.
func CheckBrokerEffectiveFiles(opts EffectiveFilesOptions) error {
	configPath := strings.TrimSpace(opts.BrokerConfigPath)
	if configPath == "" {
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read broker configuration %q: %w", configPath, err)
	}
	parsed, err := conf.ParseString(string(data), configPath)
	if err != nil {
		return fmt.Errorf("parse broker configuration %q: %w", configPath, err)
	}

	passwordFile, hasPasswordFile := effectiveDirective(parsed, "password_file")
	aclFile, hasACLFile := effectiveDirective(parsed, "acl_file")

	if !hasPasswordFile {
		return &EffectiveFilesError{
			Directive:    "password_file",
			BrokerPath:   "",
			ExpectedPath: opts.PasswdPath,
			Reason:       "the broker configuration declares no password_file",
		}
	}
	if !hasACLFile {
		return &EffectiveFilesError{
			Directive:    "acl_file",
			BrokerPath:   "",
			ExpectedPath: opts.ACLPath,
			Reason:       "the broker configuration declares no acl_file",
		}
	}

	expectedPasswd := strings.TrimSpace(opts.PasswdPath)
	if expectedPasswd == "" {
		return fmt.Errorf("deploy passwd_path is empty; set it to the password file the broker reads")
	}
	if mapped := mapBrokerPath(passwordFile, opts.BrokerConfigDir, filepath.Dir(expectedPasswd)); !samePath(mapped, expectedPasswd) {
		return &EffectiveFilesError{
			Directive:    "password_file",
			BrokerPath:   passwordFile,
			ExpectedPath: expectedPasswd,
			Reason:       "the broker reads a different password file than the one MCM writes",
		}
	}

	expectedACL := strings.TrimSpace(opts.ACLPath)
	if expectedACL == "" {
		return fmt.Errorf("deploy acl_path is empty; set it to the ACL file the broker reads")
	}
	if mapped := mapBrokerPath(aclFile, opts.BrokerConfigDir, filepath.Dir(expectedACL)); !samePath(mapped, expectedACL) {
		return &EffectiveFilesError{
			Directive:    "acl_file",
			BrokerPath:   aclFile,
			ExpectedPath: expectedACL,
			Reason:       "the broker reads a different ACL file than the one MCM writes",
		}
	}
	return nil
}

// effectiveDirective returns the value of a broker-wide directive.
//
// Only directives outside of a listener block are broker-wide: a
// per-listener `acl_file` applies to that listener alone and does not
// replace the broker-wide file MCM manages.
func effectiveDirective(parsed *conf.File, key string) (string, bool) {
	depth := 0
	for _, item := range parsed.Items {
		switch item.Kind {
		case conf.ItemBlockOpen:
			depth++
			continue
		case conf.ItemBlockClose:
			if depth > 0 {
				depth--
			}
			continue
		}
		if depth != 0 || item.Kind != conf.ItemDirective || item.Key != key {
			continue
		}
		if len(item.Values) == 0 {
			continue
		}
		return strings.TrimSpace(item.Values[0]), true
	}
	return "", false
}

// mapBrokerPath translates a path from the broker's namespace into the
// MCM namespace.
//
// The broker and MCM usually mount the same configuration volume under
// different paths: the broker reads /mosquitto/config/passwd while MCM
// writes /var/lib/mosquitto-config/passwd. Both names refer to the same
// file, so the comparison must happen in one namespace.
func mapBrokerPath(brokerPath, brokerDir, mcmDir string) string {
	brokerPath = strings.TrimSpace(brokerPath)
	brokerDir = strings.TrimSpace(brokerDir)
	if brokerDir == "" || mcmDir == "" {
		return filepath.Clean(brokerPath)
	}
	cleanBrokerDir := filepath.Clean(brokerDir)
	if brokerPath == cleanBrokerDir {
		return filepath.Clean(mcmDir)
	}
	prefix := cleanBrokerDir + string(filepath.Separator)
	if !strings.HasPrefix(brokerPath, prefix) {
		return filepath.Clean(brokerPath)
	}
	return filepath.Join(mcmDir, strings.TrimPrefix(brokerPath, prefix))
}

// samePath compares two paths after cleaning them. An empty path never
// matches.
func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}
