// Package gitea parses Gitea/Forgejo webhook payloads (they share a schema)
// and formats them as markdown notifications. The event type comes from the
// X-Gitea-Event / X-Forgejo-Event header; the body is JSON.
package gitea

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/thomas-maurice/matrix-notifier/internal/notify"
)

type user struct {
	Login string `json:"login"`
	Name  string `json:"name"`
}

type repo struct {
	FullName string `json:"full_name"`
	HTMLURL  string `json:"html_url"`
}

type commit struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	URL     string `json:"url"`
	Author  user   `json:"author"`
}

type payload struct {
	Action  string   `json:"action"`
	Ref     string   `json:"ref"`
	RefType string   `json:"ref_type"`
	Repo    repo     `json:"repository"`
	Pusher  user     `json:"pusher"`
	Sender  user     `json:"sender"`
	Commits []commit `json:"commits"`

	PullRequest *struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		Merged  bool   `json:"merged"`
		User    user   `json:"user"`
	} `json:"pull_request"`

	Issue *struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		User    user   `json:"user"`
	} `json:"issue"`

	Release *struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		HTMLURL string `json:"html_url"`
		Author  user   `json:"author"`
	} `json:"release"`
}

// Parse reads a Gitea/Forgejo webhook from an HTTP request.
func Parse(r *http.Request) (notify.Notification, error) {
	event := r.Header.Get("X-Gitea-Event")
	if event == "" {
		event = r.Header.Get("X-Forgejo-Event")
	}
	if event == "" {
		return notify.Notification{}, fmt.Errorf("missing X-Gitea-Event/X-Forgejo-Event header")
	}
	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		return notify.Notification{}, fmt.Errorf("invalid webhook payload: %w", err)
	}
	return format(event, &p), nil
}

func format(event string, p *payload) notify.Notification {
	repo := p.Repo.FullName
	switch event {
	case "push":
		return pushNotification(p, repo)
	case "pull_request":
		return prNotification(p, repo)
	case "issues":
		return issueNotification(p, repo)
	case "release":
		return releaseNotification(p, repo)
	case "create", "delete":
		return refNotification(event, p, repo)
	default:
		// Unknown/unhandled event: a minimal but honest line rather than a
		// dropped notification.
		title := fmt.Sprintf("[%s] %s", repo, event)
		if p.Action != "" {
			title += " " + p.Action
		}
		return notify.Notification{Title: title, Body: fmt.Sprintf("Gitea `%s` event", event), Priority: 3}
	}
}

func pushNotification(p *payload, repo string) notify.Notification {
	branch := strings.TrimPrefix(p.Ref, "refs/heads/")
	who := firstNonEmpty(p.Pusher.Login, p.Pusher.Name, p.Sender.Login)
	var sb strings.Builder
	for _, c := range p.Commits {
		subject, _, _ := strings.Cut(c.Message, "\n")
		short := c.ID
		if len(short) > 8 {
			short = short[:8]
		}
		fmt.Fprintf(&sb, "- [`%s`](%s) %s", short, c.URL, subject)
		if c.Author.Name != "" {
			fmt.Fprintf(&sb, " — %s", c.Author.Name)
		}
		sb.WriteString("\n")
	}
	title := fmt.Sprintf("[%s] %d commit(s) pushed to %s by %s", repo, len(p.Commits), branch, who)
	return notify.Notification{Title: title, Body: strings.TrimRight(sb.String(), "\n"), Priority: 3}
}

func prNotification(p *payload, repo string) notify.Notification {
	if p.PullRequest == nil {
		return generic(repo, "pull_request", p.Action)
	}
	action := p.Action
	if p.Action == "closed" && p.PullRequest.Merged {
		action = "merged"
	}
	title := fmt.Sprintf("[%s] PR #%d %s: %s", repo, p.PullRequest.Number, action, p.PullRequest.Title)
	body := fmt.Sprintf("[#%d](%s) by %s", p.PullRequest.Number, p.PullRequest.HTMLURL, p.PullRequest.User.Login)
	return notify.Notification{Title: title, Body: body, Priority: prPriority(action)}
}

func issueNotification(p *payload, repo string) notify.Notification {
	if p.Issue == nil {
		return generic(repo, "issues", p.Action)
	}
	title := fmt.Sprintf("[%s] issue #%d %s: %s", repo, p.Issue.Number, p.Action, p.Issue.Title)
	body := fmt.Sprintf("[#%d](%s) by %s", p.Issue.Number, p.Issue.HTMLURL, p.Issue.User.Login)
	return notify.Notification{Title: title, Body: body, Priority: 3}
}

func releaseNotification(p *payload, repo string) notify.Notification {
	if p.Release == nil {
		return generic(repo, "release", p.Action)
	}
	name := firstNonEmpty(p.Release.Name, p.Release.TagName)
	title := fmt.Sprintf("[%s] release %s %s", repo, p.Release.TagName, p.Action)
	body := fmt.Sprintf("[%s](%s) by %s", name, p.Release.HTMLURL, p.Release.Author.Login)
	return notify.Notification{Title: title, Body: body, Priority: 4}
}

func refNotification(event string, p *payload, repo string) notify.Notification {
	verb := "created"
	if event == "delete" {
		verb = "deleted"
	}
	who := firstNonEmpty(p.Sender.Login, p.Pusher.Login)
	title := fmt.Sprintf("[%s] %s %s %s by %s", repo, p.RefType, p.Ref, verb, who)
	return notify.Notification{Title: title, Priority: 3}
}

func generic(repo, event, action string) notify.Notification {
	return notify.Notification{Title: fmt.Sprintf("[%s] %s %s", repo, event, action), Priority: 3}
}

func prPriority(action string) int {
	if action == "merged" {
		return 4
	}
	return 3
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return "someone"
}
