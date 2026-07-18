package gitea

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, event, body string) (title, msgBody string) {
	t.Helper()
	r := httptest.NewRequest("POST", "/gitea", strings.NewReader(body))
	r.Header.Set("X-Gitea-Event", event)
	n, err := Parse(r)
	require.NoError(t, err)
	return n.Title, n.Body
}

// The event type lives in the header, not the body — a missing header must be
// a clear error, not a misparse.
func TestMissingEventHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/gitea", strings.NewReader(`{}`))
	_, err := Parse(r)
	require.Error(t, err)
}

// Forgejo sends the same schema under a different header name.
func TestForgejoHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/forgejo", strings.NewReader(
		`{"ref":"refs/heads/main","repository":{"full_name":"me/repo"},"pusher":{"login":"me"},"commits":[]}`))
	r.Header.Set("X-Forgejo-Event", "push")
	n, err := Parse(r)
	require.NoError(t, err)
	assert.Contains(t, n.Title, "me/repo")
}

func TestPush(t *testing.T) {
	title, body := parse(t, "push", `{
		"ref":"refs/heads/main","repository":{"full_name":"me/repo"},"pusher":{"login":"thomas"},
		"commits":[{"id":"abcdef1234567890","message":"fix the thing\n\ndetails","url":"http://git/c/abc","author":{"name":"Thomas"}}]}`)
	// Branch, count and author must be visible at a glance.
	assert.Contains(t, title, "1 commit(s) pushed to main by thomas")
	assert.Contains(t, body, "`abcdef12`") // short SHA
	assert.Contains(t, body, "fix the thing")
	assert.NotContains(t, body, "details") // only the subject line
}

func TestPullRequestMergedVsClosed(t *testing.T) {
	// A merged PR must read "merged", not "closed" — the distinction matters.
	title, _ := parse(t, "pull_request", `{"action":"closed","repository":{"full_name":"me/repo"},
		"pull_request":{"number":42,"title":"Add feature","merged":true,"user":{"login":"thomas"},"html_url":"http://git/pr/42"}}`)
	assert.Contains(t, title, "PR #42 merged: Add feature")

	title, _ = parse(t, "pull_request", `{"action":"closed","repository":{"full_name":"me/repo"},
		"pull_request":{"number":43,"title":"Rejected","merged":false,"user":{"login":"x"},"html_url":"http://git/pr/43"}}`)
	assert.Contains(t, title, "PR #43 closed")
}

func TestIssueAndRelease(t *testing.T) {
	title, body := parse(t, "issues", `{"action":"opened","repository":{"full_name":"me/repo"},
		"issue":{"number":7,"title":"Bug","user":{"login":"reporter"},"html_url":"http://git/i/7"}}`)
	assert.Contains(t, title, "issue #7 opened: Bug")
	assert.Contains(t, body, "reporter")

	title, _ = parse(t, "release", `{"action":"published","repository":{"full_name":"me/repo"},
		"release":{"tag_name":"v1.2.0","name":"Big release","author":{"login":"thomas"},"html_url":"http://git/r/1"}}`)
	assert.Contains(t, title, "release v1.2.0 published")
}

// Forgejo action_run_* payloads carry the repository inside the run object,
// not at the top level — the title must still name the repo, and a failure
// must outrank routine events so it stands out in the room.
func TestActionRunFailure(t *testing.T) {
	body := `{"action":"failure","run":{
		"title":"fix the thing","workflow_id":"test.yml","index_in_repo":12,
		"prettyref":"master","html_url":"http://git/me/repo/actions/runs/12",
		"repository":{"full_name":"me/repo"},"trigger_user":{"login":"thomas"}}}`
	r := httptest.NewRequest("POST", "/forgejo", strings.NewReader(body))
	r.Header.Set("X-Forgejo-Event", "action_run_failure")
	n, err := Parse(r)
	require.NoError(t, err)
	assert.Contains(t, n.Title, "[me/repo] CI failed: fix the thing (master)")
	assert.Contains(t, n.Body, "test.yml run #12")
	assert.Contains(t, n.Body, "thomas")
	assert.Equal(t, 5, n.Priority)

	r = httptest.NewRequest("POST", "/forgejo", strings.NewReader(body))
	r.Header.Set("X-Forgejo-Event", "action_run_recover")
	n, err = Parse(r)
	require.NoError(t, err)
	assert.Contains(t, n.Title, "CI recovered")
	assert.Equal(t, 3, n.Priority, "recovery is an all-clear, it must not page like a failure")
}

// A malformed action_run payload without the run object must degrade to the
// generic line, not panic on a nil pointer.
func TestActionRunMissingRun(t *testing.T) {
	title, _ := parse(t, "action_run_failure", `{"action":"failure","repository":{"full_name":"me/repo"}}`)
	assert.Contains(t, title, "action_run_failure")
}

func TestUnknownEventStillNotifies(t *testing.T) {
	// An unhandled event must produce something, not silently drop.
	title, _ := parse(t, "repository", `{"action":"created","repository":{"full_name":"me/repo"}}`)
	assert.Contains(t, title, "me/repo")
	assert.Contains(t, title, "repository")
}
