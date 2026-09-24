package conf

import (
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff returns a unified diff between current and rendered. Both inputs
// are pre-rendered bytes (Render() output) so the diff is stable across
// parsing variations. Context is 3 lines, mirroring the deploy service's
// passwd/acl diffs.
func Diff(fromFile, toFile string, current, rendered []byte) (string, error) {
	diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(current)),
		B:        difflib.SplitLines(string(rendered)),
		FromFile: fromFile,
		ToFile:   toFile,
		Context:  3,
	})
	if err != nil {
		return "", fmt.Errorf("unified diff: %w", err)
	}
	return diff, nil
}
