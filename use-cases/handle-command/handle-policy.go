/*
handle-policy loads Telegram-safe HTML from assets/policies/<name>.html.
Used via RootUseCase so policy-requested analytics fire in one place.
*/

package handlecommand

import (
	"context"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

type HandlePolicyUseCase struct{}

func NewHandlePolicyUseCase() *HandlePolicyUseCase {
	return &HandlePolicyUseCase{}
}

func (u *HandlePolicyUseCase) Handle(ctx context.Context, policyName string) (string, error) {
	_ = ctx
	policyName = strings.TrimSpace(policyName)
	if policyName == "" {
		policyName = "help"
	}
	path := filepath.Join(".", "assets", "policies", policyName+".html")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read policy %s: %w", policyName, err)
	}
	out := string(raw)
	if policyName == "community" {
		out = renderCommunityPolicyHTML(out)
	}
	return out, nil
}

func renderCommunityPolicyHTML(body string) string {
	linkLine := `<i>Set COMMUNITY_URL for the invite link.</i>`
	if u := strings.TrimSpace(os.Getenv("COMMUNITY_URL")); u != "" {
		linkLine = `<a href="` + html.EscapeString(u) + `">` + html.EscapeString("Open community") + `</a>`
	}
	return strings.ReplaceAll(body, "{{LINK_LINE}}", linkLine)
}
