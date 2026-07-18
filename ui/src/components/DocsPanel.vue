<script setup lang="ts">
// Static endpoint documentation. Keep this page in sync with the ingest
// parsers (internal/ingest/*) and the README whenever an endpoint or its
// configuration changes — it is part of the repo's maintenance contract.
const origin = window.location.origin
</script>

<template>
  <div class="mb-4">
    <h4 class="mb-3"><i class="fa-solid fa-book me-2"></i>Webhook endpoints</h4>
    <p class="text-secondary">
      Every endpoint authenticates with an <strong>ingest token</strong>
      (<code>mn_...</code>, minted on the Tokens tab). A token belongs to a
      channel — that channel's Matrix room is where the notification lands.
      Notification rooms must be <strong>encrypted and named</strong>: a
      nameless two-member room is indistinguishable from a direct message,
      so it is treated as one and not offered for channel mapping.
      Tokens can be restricted to a single endpoint kind, carry an optional
      prefix prepended to every message, and are rate limited per token.
      Unless noted otherwise, the token can be presented as
      <code>?token=mn_...</code>, an <code>Authorization: Bearer mn_...</code>
      header, or an <code>X-Gotify-Key</code> header.
    </p>

    <div class="card mb-3">
      <div class="card-header">
        <i class="fa-solid fa-message me-2"></i><strong>Gotify</strong>
        — <code>POST /message</code>
        <span class="badge text-bg-secondary ms-2">kind: gotify</span>
      </div>
      <div class="card-body">
        <p>
          Drop-in replacement for a Gotify server: anything that can push to
          Gotify can push here unmodified. Accepts JSON, urlencoded and
          multipart form bodies with <code>title</code>,
          <code>message</code> (rendered as markdown) and
          <code>priority</code> (0–10, ≥8 is emergency).
        </p>
        <pre class="bg-body-tertiary p-2 rounded mb-0"><code>curl -X POST '{{ origin }}/message?token=mn_...' \
  -F title='Backup done' -F message='**All good**' -F priority=3</code></pre>
      </div>
    </div>

    <div class="card mb-3">
      <div class="card-header">
        <i class="fa-solid fa-fire me-2"></i><strong>Prometheus Alertmanager</strong>
        — <code>POST /alertmanager</code>
        <span class="badge text-bg-secondary ms-2">kind: alertmanager</span>
      </div>
      <div class="card-body">
        <p>
          Webhook receiver (payload v4). Formats firing/resolved counts,
          severities, summaries and generator links — anchored to the
          alert's firing window (graph tab, ending shortly after the onset)
          so they still show the trigger when clicked late. Priority comes
          from the
          firing alerts' <code>severity</code> label:
          <code>critical</code> → 8, <code>warning</code> → 5, otherwise 3.
          If the channel has charts enabled and an alert carries the
          <code>chart: "true"</code> annotation, a Prometheus graph of the
          alert expression is attached as an image.
        </p>
        <p class="mb-1">Alertmanager configuration:</p>
        <pre class="bg-body-tertiary p-2 rounded mb-0"><code>receivers:
  - name: matrix-notifier
    webhook_configs:
      - url: {{ origin }}/alertmanager?token=mn_...
        send_resolved: true</code></pre>
      </div>
    </div>

    <div class="card mb-3">
      <div class="card-header">
        <i class="fa-solid fa-code-branch me-2"></i><strong>Gitea / Forgejo</strong>
        — <code>POST /gitea</code> or <code>POST /forgejo</code>
        <span class="badge text-bg-secondary ms-2">kind: gitea</span>
      </div>
      <div class="card-body">
        <p>
          Webhook receiver for push, pull-request, issue, release and
          branch/tag events (the event type is read from the
          <code>X-Gitea-Event</code> / <code>X-Forgejo-Event</code> header),
          plus Forgejo's CI events (Forgejo ≥ v12):
          <code>action_run_failure</code> renders at priority 5,
          <code>action_run_recover</code> and <code>_success</code> at 3.
          Merged PRs and releases get priority 4, everything else 3.
        </p>
        <p class="mb-1">
          In the repo (or org) settings, add a webhook of type
          <strong>Forgejo</strong> / <strong>Gitea</strong>:
        </p>
        <ul class="mb-2">
          <li>Target URL: <code>{{ origin }}/forgejo</code></li>
          <li>Method <code>POST</code>, content type <code>application/json</code></li>
          <li>
            Authorization Header: <code>Bearer mn_...</code> — preferred over
            <code>?token=</code> in the URL, which ends up in proxy logs
          </li>
          <li>
            Secret: leave <em>empty</em> — the HMAC signature is not
            verified; the ingest token is the authentication
          </li>
          <li>
            For CI-failure notifications: trigger on "Custom Events…" and
            tick <em>Action Run Failure</em> (plus <em>Recover</em> for the
            all-clear). Optionally filter to your default branch.
          </li>
        </ul>
      </div>
    </div>

    <div class="card mb-3">
      <div class="card-header">
        <i class="fa-brands fa-slack me-2"></i><strong>Slack incoming webhook</strong>
        — <code>POST /slack</code>
        <span class="badge text-bg-secondary ms-2">kind: slack</span>
      </div>
      <div class="card-body">
        <p>
          For tools that only speak Slack webhooks (TrueNAS SCALE alert
          services, Uptime Kuma, ...). Accepts a JSON body or Slack's legacy
          <code>payload=</code> form field; reads <code>text</code>,
          <code>blocks</code> (header/section) and <code>attachments</code>
          (title/text/fallback). <code>username</code> becomes the
          notification title. An attachment color of <code>danger</code>
          raises the priority to 5, <code>warning</code> to 4. Slack mrkdwn
          links (<code>&lt;url|label&gt;</code>) and escaped entities are
          converted to markdown. Responds with Slack's literal
          <code>ok</code>.
        </p>
        <p>
          Slack senders cannot set headers, so the token rides in the URL:
          configure the webhook URL as
          <code>{{ origin }}/slack?token=mn_...</code>. On TrueNAS SCALE:
          System → Alert Settings → Add alert service of type
          <em>Slack</em>, paste that URL as the webhook URL.
        </p>
        <pre class="bg-body-tertiary p-2 rounded mb-0"><code>curl -X POST '{{ origin }}/slack?token=mn_...' \
  -H 'Content-Type: application/json' \
  -d '{"username":"TrueNAS","text":"Pool tank is healthy again"}'</code></pre>
      </div>
    </div>

    <h4 class="mb-3 mt-4"><i class="fa-solid fa-clock-rotate-left me-2"></i>Room retention (purging old notifications)</h4>
    <div class="card mb-3">
      <div class="card-body">
        <p>
          Notification rooms grow forever by default. Matrix supports
          per-room retention: the homeserver deletes events older than a
          lifetime you set on the room. This is homeserver-side — the bot
          needs no configuration.
        </p>
        <p class="mb-1">
          <strong>1. Enable retention on the homeserver</strong> (Synapse
          <code>homeserver.yaml</code> — it is off by default; the purge job
          runs in the background):
        </p>
        <pre class="bg-body-tertiary p-2 rounded"><code>retention:
  enabled: true</code></pre>
        <p class="mb-1">
          <strong>2. Set the policy on the room</strong> with an
          <code>m.room.retention</code> state event
          (<code>max_lifetime</code> is in milliseconds — the example is 7
          days). In Element: enable developer mode, then
          <code>/devtools</code> in the room → Send custom state event.
          Or with any access token of a room admin:
        </p>
        <pre class="bg-body-tertiary p-2 rounded"><code>curl -X PUT \
  'https://homeserver/_matrix/client/v3/rooms/!roomid:server/state/m.room.retention' \
  -H 'Authorization: Bearer &lt;access token&gt;' \
  -H 'Content-Type: application/json' \
  -d '{"max_lifetime": 604800000}'</code></pre>
        <p class="mb-0">
          Deleting events does not automatically free attachment media;
          pair it with Synapse's <code>media_retention</code> setting
          (<code>local_media_lifetime</code>) so charts and images are
          purged too. One-off cleanups can use Synapse's admin
          <em>purge history</em> API instead.
        </p>
      </div>
    </div>
  </div>
</template>
