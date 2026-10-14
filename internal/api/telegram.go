package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/devai-io/forge/internal/assistant"
	"github.com/devai-io/forge/internal/store"
	"github.com/devai-io/forge/internal/telegram"
)

// Telegram: Forge in a chat with its owner. The server long-polls the Bot
// API (no webhook, nothing to expose), and only ever reads from or writes to
// the one chat set in Settings → Telegram:
//
//   - an interactive run's prompts arrive with buttons (allow / deny /
//     approve / pick an option); replying to one answers it in words;
//   - a run that finishes, or waits for a reply, says so; replying to that
//     message is the follow-up;
//   - the scheduled check-up is posted;
//   - anything else typed is a message to the Assistant, whose answer comes
//     back in the chat; /new starts a fresh chat, and a few commands
//     (/tasks, /runs, /checkup, /remember, /recall) answer without it.

const telegramTag = "integration:telegram"

var telegramTokenRe = regexp.MustCompile(`^\d{5,15}:[A-Za-z0-9_-]{30,64}$`)

// tgState is what the bot loop knows that is not in the database.
type tgState struct {
	mu        sync.Mutex
	lastError string
	lastErrAt time.Time
	lastOK    time.Time
	bot       string // @username, once getMe has answered
	following map[int64]bool
}

func (s *Server) telegramToken(ctx context.Context) (string, error) {
	if !s.box.Available() {
		return "", telegram.ErrNoToken
	}
	k, err := s.store.IntegrationSecret(ctx, s.box, telegramTag)
	if errors.Is(err, store.ErrNotFound) {
		return "", telegram.ErrNoToken
	}
	return k, err
}

// tgOn returns the settings when the bot may talk: enabled, a chat set and
// a token in the vault.
func (s *Server) tgOn(ctx context.Context) (store.TelegramSettings, bool) {
	set, err := s.store.TelegramSettings(ctx)
	if err != nil || !set.Enabled || set.ChatID == 0 {
		return set, false
	}
	if k, err := s.telegramToken(ctx); err != nil || k == "" {
		return set, false
	}
	return set, true
}

func (s *Server) tgFail(err error) {
	s.tgs.mu.Lock()
	s.tgs.lastError, s.tgs.lastErrAt = err.Error(), time.Now()
	s.tgs.mu.Unlock()
	slog.Warn("telegram", "err", err)
}

// tgSend posts a message to the owner's chat (Markdown, rendered) and
// records what it stands for. Long text goes as several messages; the link
// is on the last one, the one a reply naturally goes to.
func (s *Server) tgSend(ctx context.Context, chatID int64, md string, kb telegram.Keyboard, link *store.TelegramLink) *telegram.Message {
	parts := telegram.Split(md, telegram.MaxText)
	if len(parts) == 0 {
		return nil
	}
	var last *telegram.Message
	for i, p := range parts {
		send := telegram.Send{ChatID: chatID, Text: telegram.HTML(p), HTML: true}
		if i == len(parts)-1 {
			send.Keyboard = kb
		}
		m, err := s.tg.Send(ctx, send)
		if err != nil {
			s.tgFail(err)
			return nil
		}
		last = m
	}
	if link != nil && last != nil {
		link.TgChat, link.MessageID = chatID, last.MessageID
		if err := s.store.AddTelegramLink(ctx, *link); err != nil {
			slog.Error("telegram link", "err", err)
		}
	}
	return last
}

// tgAsync runs a notification off the request that triggered it: a slow
// Telegram must never hold up a machine's report.
func (s *Server) tgAsync(fn func(ctx context.Context)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		fn(ctx)
	}()
}

// ── The poll loop ─────────────────────────────────────────────────────────

// RunTelegram polls the bot for messages and button presses until ctx ends.
// It idles while Telegram is off or has no token, and picks up a change in
// Settings within half a minute.
func (s *Server) RunTelegram(ctx context.Context) {
	var offset int64
	wait := func(d time.Duration) bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
			return true
		}
	}
	commandsSet := false
	for ctx.Err() == nil {
		set, err := s.store.TelegramSettings(ctx)
		token, _ := s.telegramToken(ctx)
		if err != nil || !set.Enabled || token == "" {
			commandsSet = false
			if !wait(30 * time.Second) {
				return
			}
			continue
		}
		if !commandsSet {
			if err := s.tg.SetCommands(ctx, tgCommands); err == nil {
				commandsSet = true
			}
		}
		ups, err := s.tg.GetUpdates(ctx, offset, s.tgPoll)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.tgFail(err)
			if !wait(10 * time.Second) {
				return
			}
			continue
		}
		s.tgs.mu.Lock()
		s.tgs.lastOK = time.Now()
		s.tgs.mu.Unlock()
		for _, u := range ups {
			offset = u.UpdateID + 1
			hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			s.handleTelegram(hctx, set, u)
			cancel()
		}
	}
}

var tgCommands = []telegram.Command{
	{Command: "new", Description: "Start a fresh Assistant chat"},
	{Command: "stop", Description: "Stop the Assistant's current turn"},
	{Command: "tasks", Description: "Open tasks [PROJECT]"},
	{Command: "runs", Description: "Active and recent runs"},
	{Command: "checkup", Description: "Today's check-up actions"},
	{Command: "bots", Description: "Project bots"},
	{Command: "bot", Description: "PROJECT message — talk to a project's bot"},
	{Command: "remember", Description: "[PROJECT] text — save a memory"},
	{Command: "recall", Description: "[PROJECT] words — search memory"},
	{Command: "help", Description: "What this bot does"},
}

func (s *Server) handleTelegram(ctx context.Context, set store.TelegramSettings, u telegram.Update) {
	if cq := u.CallbackQuery; cq != nil {
		if cq.Message == nil || set.ChatID == 0 || cq.Message.Chat.ID != set.ChatID {
			_ = s.tg.AnswerCallback(ctx, cq.ID, "This chat is not linked to Forge.")
			return
		}
		s.tgCallback(ctx, set, cq)
		return
	}
	m := u.Message
	if m == nil || m.From == nil || m.From.IsBot {
		return
	}
	if set.ChatID == 0 {
		// Not linked yet: tell the owner the id to enter. Nothing else is
		// said or done for an unlinked chat.
		s.tgSend(ctx, m.Chat.ID, fmt.Sprintf("This chat is not linked to Forge yet. Its id is `%d`: enter it under "+
			"Settings → Telegram (or run `forge telegram chat %d` on the server).", m.Chat.ID, m.Chat.ID), nil, nil)
		return
	}
	if m.Chat.ID != set.ChatID {
		slog.Info("telegram: ignored a message from an unlinked chat", "chat", m.Chat.ID)
		return
	}
	text := strings.TrimSpace(m.Text)
	if text == "" {
		text = strings.TrimSpace(m.Caption)
	}
	if text == "" {
		s.tgSend(ctx, set.ChatID, "I can only read text for now.", nil, nil)
		return
	}
	if strings.HasPrefix(text, "/") {
		s.tgCommand(ctx, set, text)
		return
	}
	if m.ReplyTo != nil {
		if l, err := s.store.TelegramLinkFor(ctx, set.ChatID, m.ReplyTo.MessageID); err == nil {
			s.tgReply(ctx, set, l, text)
			return
		}
	}
	s.tgAssistant(ctx, set, 0, text)
}

// ── Commands ──────────────────────────────────────────────────────────────

func (s *Server) tgCommand(ctx context.Context, set store.TelegramSettings, text string) {
	cmd, rest, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(strings.ToLower(cmd), "@") // /tasks@my_bot in a group
	rest = strings.TrimSpace(rest)
	say := func(md string) { s.tgSend(ctx, set.ChatID, md, nil, nil) }
	switch cmd {
	case "/start", "/help":
		say("**Forge** — " + s.cfg.PublicURL + "\n\n" +
			"- Prompts from interactive runs come here with buttons; reply to one to answer it in words (or to deny with a reason).\n" +
			"- A finished run or one waiting for you says so; reply to it to continue the session.\n" +
			"- Anything else you write goes to the Assistant. /new starts a fresh chat, /stop stops it.\n" +
			"- /tasks [PROJECT], /runs, /checkup\n" +
			"- /bots lists the project bots; /bot PROJECT message talks to one (reply to its answer to go on)\n" +
			"- /remember [PROJECT] text — save a memory (no project: about you, every project)\n" +
			"- /recall [PROJECT] words — search memory")
	case "/new":
		if err := s.store.SetTelegramChat(ctx, 0); err != nil {
			say("Could not start a new chat: " + err.Error())
			return
		}
		if rest != "" {
			s.tgAssistant(ctx, set, 0, rest)
			return
		}
		say("Next message starts a new Assistant chat.")
	case "/stop":
		if id := s.store.TelegramChat(ctx); id != 0 {
			s.assistant.Stop(ctx, id)
			say("Stopped.")
			return
		}
		say("Nothing is running.")
	case "/tasks":
		say(s.tgTasks(ctx, strings.ToUpper(rest)))
	case "/bots":
		say(s.tgBots(ctx))
	case "/bot":
		key, msg, _ := strings.Cut(rest, " ")
		if key == "" || strings.TrimSpace(msg) == "" {
			say("Usage: /bot PROJECT message")
			return
		}
		s.tgBot(ctx, set, strings.ToUpper(key), strings.TrimSpace(msg))
	case "/runs":
		say(s.tgRuns(ctx))
	case "/checkup":
		c, err := s.store.LatestCheckup(ctx)
		if err != nil {
			say("No check-up yet.")
			return
		}
		say(checkupMarkdown(c, s.cfg.PublicURL))
	case "/remember":
		key, content := s.tgProjectArg(ctx, rest)
		saved, err := s.store.CreateMemory(ctx, store.MemoryInput{ProjectKey: key, Content: content, Source: "telegram", Origin: "telegram"})
		if err != nil {
			say("Not saved: " + err.Error())
			return
		}
		say(memorySavedMarkdown(saved))
	case "/recall":
		key, q := s.tgProjectArg(ctx, rest)
		f := store.MemoryFilter{ProjectKey: key, WithGlobal: key != "", Query: q, Limit: 10}
		ms, err := s.store.ListMemories(ctx, f)
		if err != nil {
			say("Search failed: " + err.Error())
			return
		}
		if len(ms) == 0 {
			say("Nothing found.")
			return
		}
		var b strings.Builder
		for _, m := range ms {
			scope := "you"
			if m.ProjectKey != nil {
				scope = *m.ProjectKey
			}
			fmt.Fprintf(&b, "- %s — %s\n", scope, m.Line(300))
		}
		say(b.String())
	default:
		say("Unknown command. /help lists them.")
	}
}

// tgProjectArg splits "SHOP the rest" into a project key and the rest when
// the first word is a project key; otherwise there is no project.
func (s *Server) tgProjectArg(ctx context.Context, arg string) (string, string) {
	first, rest, _ := strings.Cut(arg, " ")
	if first != "" && strings.ToUpper(first) == first {
		if _, err := s.store.ProjectIDByKey(ctx, first); err == nil {
			return first, strings.TrimSpace(rest)
		}
	}
	return "", arg
}

func (s *Server) tgTasks(ctx context.Context, project string) string {
	tasks, err := s.store.ListTasks(ctx, store.TaskFilter{ProjectKey: project, Open: true, Limit: 25, Today: store.TodayFor("UTC")})
	if err != nil {
		return "Could not list tasks: " + err.Error()
	}
	if len(tasks) == 0 {
		return "No open tasks."
	}
	var b strings.Builder
	for _, t := range tasks {
		focus := ""
		if t.Focus {
			focus = " ★"
		}
		fmt.Fprintf(&b, "- [%s](%s/p/%s?tab=list) %s/%s: %s%s\n", t.Ref, s.cfg.PublicURL, t.ProjectKey,
			strings.ReplaceAll(t.Status, "_", " "), t.Priority, t.Title, focus)
	}
	return b.String()
}

func (s *Server) tgRuns(ctx context.Context) string {
	runs, err := s.store.ListRuns(ctx, store.RunFilter{Limit: 8})
	if err != nil {
		return "Could not list runs: " + err.Error()
	}
	if len(runs) == 0 {
		return "No runs yet."
	}
	var b strings.Builder
	for _, r := range runs {
		what := r.Command
		if r.Kind == "agent" {
			what = telegram.Clip(strings.Join(strings.Fields(r.Prompt), " "), 70)
		}
		fmt.Fprintf(&b, "- [#%d](%s/agents/runs/%d) %s %s · %s — %s\n", r.ID, s.cfg.PublicURL, r.ID, runIcon(r.Status),
			r.Status, runWhere(&r), what)
	}
	return b.String()
}

func runIcon(status string) string {
	return map[string]string{"queued": "⏳", "running": "▶️", "succeeded": "✅", "failed": "❌", "cancelled": "⏹"}[status]
}

func runWhere(r *store.Run) string {
	where := r.RunnerName
	if r.ProjectKey != "" {
		where = r.ProjectKey + "/" + r.RepoName + " on " + r.RunnerName
	}
	return where
}

func checkupMarkdown(c *store.Checkup, publicURL string) string {
	var b strings.Builder
	if c.ActionsTotal == 0 {
		fmt.Fprintf(&b, "🩺 **Check-up %s: all clear** (%d checks)\n", c.Date, c.Counts.OK)
	} else {
		fmt.Fprintf(&b, "🩺 **Check-up %s** — %d failing, %d warnings\n\n", c.Date, c.Counts.Fail, c.Counts.Warn)
	}
	n := 0
	for _, it := range c.Items {
		if it.Severity == "ok" || it.Done {
			continue
		}
		if n++; n > 15 {
			fmt.Fprintf(&b, "- …and more\n")
			break
		}
		mark := "!"
		if it.Severity == "fail" {
			mark = "✗"
		}
		line := fmt.Sprintf("- %s %s", mark, it.Title)
		if it.Action != "" {
			line += " → " + it.Action
		}
		b.WriteString(telegram.Clip(line, 300) + "\n")
	}
	fmt.Fprintf(&b, "\n[Open the check-up](%s/checkup)", publicURL)
	return b.String()
}

func memorySavedMarkdown(saved *store.MemorySaved) string {
	scope := "about you"
	if saved.Memory.ProjectKey != nil {
		scope = "in " + *saved.Memory.ProjectKey
	}
	if saved.Duplicate {
		return fmt.Sprintf("Already remembered %s: m%d.", scope, saved.Memory.ID)
	}
	out := fmt.Sprintf("Remembered %s as m%d.", scope, saved.Memory.ID)
	if len(saved.Similar) > 0 {
		out += "\nSimilar ones:"
		for _, m := range saved.Similar {
			out += "\n- " + m.Line(200)
		}
	}
	return out
}

// ── Buttons ───────────────────────────────────────────────────────────────
//
// Callback data: "p:<prompt>:<decision>" answers a prompt, with decision
// "o<n>" picking option n of a one-question prompt; "e:<run>" ends an
// interactive session.

var promptDecisionLabel = map[string]string{
	"allow": "Allowed", "allow_always": "Allowed for this session", "deny": "Denied",
	"approve": "Plan approved", "approve_edits": "Plan approved, edits accepted",
}

func (s *Server) tgCallback(ctx context.Context, set store.TelegramSettings, cq *telegram.CallbackQuery) {
	parts := strings.Split(cq.Data, ":")
	done := func(toast, status string) {
		_ = s.tg.AnswerCallback(ctx, cq.ID, toast)
		if status != "" {
			_ = s.tg.Edit(ctx, set.ChatID, cq.Message.MessageID, telegram.Escape(cq.Message.Text)+"\n\n<b>→ "+telegram.Escape(status)+"</b>", true, nil)
		}
	}
	switch {
	case len(parts) == 3 && parts[0] == "p":
		if !set.Prompts {
			done("Answering prompts from Telegram is off.", "")
			return
		}
		pid, _ := strconv.ParseInt(parts[1], 10, 64)
		p, err := s.store.PromptByID(ctx, pid)
		if err != nil {
			done("That prompt is gone.", "gone")
			return
		}
		ans := store.PromptAnswer{Decision: parts[2]}
		label := promptDecisionLabel[parts[2]]
		if strings.HasPrefix(parts[2], "o") {
			n, _ := strconv.Atoi(parts[2][1:])
			qs := promptQuestions(p.Input)
			if len(qs) != 1 || n < 0 || n >= len(qs[0].Options) {
				done("That option is gone.", "")
				return
			}
			ans = store.PromptAnswer{Decision: "answer", Answers: map[string]string{qs[0].Question: qs[0].Options[n].Label}}
			label = "Answered: " + qs[0].Options[n].Label
		}
		if err := s.tgAnswer(ctx, p, ans); err != nil {
			done(err.Error(), tgAnswerStatus(err))
			return
		}
		done("Sent to the session", label)
	case len(parts) == 2 && parts[0] == "e":
		id, _ := strconv.ParseInt(parts[1], 10, 64)
		if err := s.store.EndRunSession(ctx, id); err != nil {
			done("Could not end it: "+tgErrText(err), "")
			return
		}
		s.inboxes.notify(id)
		s.tgSecurity(ctx, "telegram_end_run", fmt.Sprintf("run %d", id))
		done("Ending the session", "Session ending")
	default:
		done("Unknown button.", "")
	}
}

// tgAnswer answers a prompt as the web app would.
func (s *Server) tgAnswer(ctx context.Context, p *store.RunPrompt, ans store.PromptAnswer) error {
	if p.Status != "pending" {
		return fmt.Errorf("%w: already %s", store.ErrConflict, p.Status)
	}
	if _, err := s.store.AnswerPrompt(ctx, p.RunID, p.ID, ans); err != nil {
		return err
	}
	s.inboxes.notify(p.RunID)
	s.tgSecurity(ctx, "telegram_answer", fmt.Sprintf("run %d prompt %d: %s %s", p.RunID, p.ID, p.ToolName, ans.Decision))
	return nil
}

func tgAnswerStatus(err error) string {
	if errors.Is(err, store.ErrConflict) {
		return "already answered"
	}
	var verr *store.ValidationError
	if errors.As(err, &verr) {
		return verr.Message
	}
	return ""
}

func tgErrText(err error) string {
	var verr *store.ValidationError
	if errors.As(err, &verr) {
		return verr.Message
	}
	if errors.Is(err, store.ErrNotFound) {
		return "not found"
	}
	return err.Error()
}

func (s *Server) tgSecurity(ctx context.Context, kind, detail string) {
	if err := s.store.LogSecurity(ctx, kind, detail, "telegram", "telegram", 0); err != nil {
		slog.Error("security log", "kind", kind, "err", err)
	}
}

// ── Replies ───────────────────────────────────────────────────────────────

// tgReply handles a reply to one of the bot's messages.
func (s *Server) tgReply(ctx context.Context, set store.TelegramSettings, l *store.TelegramLink, text string) {
	say := func(md string) { s.tgSend(ctx, set.ChatID, md, nil, nil) }
	switch l.Kind {
	case "prompt":
		if !set.Prompts || l.PromptID == nil {
			say("Answering prompts from Telegram is off.")
			return
		}
		p, err := s.store.PromptByID(ctx, *l.PromptID)
		if err != nil {
			say("That prompt is gone.")
			return
		}
		ans := store.PromptAnswer{Decision: "deny", Message: text} // a permission or a plan: no, and why
		if p.Kind == "question" {
			qs := promptQuestions(p.Input)
			lines := strings.Split(text, "\n")
			if len(qs) > 1 && len(lines) < len(qs) {
				say(fmt.Sprintf("There are %d questions: answer each on its own line.", len(qs)))
				return
			}
			ans = store.PromptAnswer{Decision: "answer", Answers: map[string]string{}}
			for i, q := range qs {
				a := text
				if len(qs) > 1 {
					a = strings.TrimSpace(lines[i])
				}
				ans.Answers[q.Question] = a
			}
		}
		if err := s.tgAnswer(ctx, p, ans); err != nil {
			say("Not sent: " + tgErrText(err))
			return
		}
		if ans.Decision == "deny" {
			say(fmt.Sprintf("Denied, with your note, in run #%d.", p.RunID))
		} else {
			say(fmt.Sprintf("Answer sent to run #%d.", p.RunID))
		}
	case "run":
		if l.RunID == nil {
			return
		}
		if err := s.store.SendRunMessage(ctx, *l.RunID, text); err != nil {
			say("Not sent: " + tgErrText(err))
			return
		}
		s.inboxes.notify(*l.RunID)
		s.tgSecurity(ctx, "telegram_run_message", fmt.Sprintf("run %d", *l.RunID))
		say(fmt.Sprintf("Sent to run #%d.", *l.RunID))
	case "chat":
		if l.ChatID == nil {
			return
		}
		if chat, err := s.store.ChatByID(ctx, *l.ChatID); err == nil && chat.ProjectKey != "" {
			s.tgBot(ctx, set, chat.ProjectKey, text)
			return
		}
		s.tgAssistant(ctx, set, *l.ChatID, text)
	}
}

// ── The Assistant ─────────────────────────────────────────────────────────

// tgAssistant sends text to an Assistant chat (chatID 0: the current
// Telegram chat, started on demand) and posts the answer when the turn ends.
func (s *Server) tgAssistant(ctx context.Context, set store.TelegramSettings, chatID int64, text string) {
	say := func(md string) { s.tgSend(ctx, set.ChatID, md, nil, nil) }
	if !set.Assistant {
		say("The Assistant is off for Telegram (Settings → Telegram). /help lists the commands.")
		return
	}
	user, err := s.store.FirstUser(ctx)
	if err != nil {
		say("No account yet.")
		return
	}
	if chatID == 0 {
		chatID = s.store.TelegramChat(ctx)
	}
	var chat *store.Chat
	if chatID != 0 {
		if chat, err = s.store.ChatByID(ctx, chatID); err != nil {
			chat = nil
		}
	}
	if chat == nil {
		cs := s.assistant.DefaultSettings(ctx)
		if err := s.assistant.Ready(ctx, cs.Engine); err != nil {
			say(tgAssistantErr(err))
			return
		}
		title := telegram.Clip(strings.Join(strings.Fields(text), " "), 60)
		if chat, err = s.store.CreateChat(ctx, title, cs); err != nil {
			say("Could not start a chat: " + err.Error())
			return
		}
	} else if chat.Busy {
		say("Still working on your last message. /stop to cancel it.")
		return
	} else if err := s.assistant.Ready(ctx, chat.Engine); err != nil {
		say(tgAssistantErr(err))
		return
	}
	_ = s.store.SetTelegramChat(ctx, chat.ID)
	m, err := s.store.AppendChatMessage(ctx, chat.ID, store.ChatMessage{Role: "user", Content: text})
	if err != nil {
		say("Could not send: " + err.Error())
		return
	}
	if err := s.assistant.Start(ctx, chat.ID, user); err != nil {
		say(tgAssistantErr(err))
		return
	}
	go s.tgFollowChat(set.ChatID, chat.ID, m.Seq)
}

func tgAssistantErr(err error) string {
	switch {
	case errors.Is(err, assistant.ErrOff):
		return "The Assistant is off: turn it on in Settings → Assistant."
	case errors.Is(err, assistant.ErrNoMachine), errors.Is(err, assistant.ErrNoKey):
		return err.Error()
	case errors.Is(err, store.ErrConflict):
		return "Still working on your last message. /stop to cancel it."
	}
	return "The Assistant failed: " + err.Error()
}

// tgFollowChat waits for the Assistant's turn to end, showing "typing…",
// then posts its answer (the last thing it said), linked so a reply goes
// back to the same chat.
func (s *Server) tgFollowChat(tgChat, chatID int64, afterSeq int) {
	s.tgFollow(tgChat, chatID, afterSeq, true, func(answer string) string { return answer })
}

// tgFollowBot posts a bot's answer to Telegram: to a message sent from
// there, or to a turn Forge started (then quietly — no "typing…" — and
// not at all when the answer is just "Nothing to do.").
func (s *Server) tgFollowBot(chatID int64, afterSeq int, key, origin string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	set, on := s.tgOn(ctx)
	cancel()
	if !on {
		return
	}
	proactive := origin == "event" || origin == "schedule"
	go s.tgFollow(set.ChatID, chatID, afterSeq, !proactive, func(answer string) string {
		if proactive && nothingToDo(answer) {
			return ""
		}
		head := "🤖 **" + key + " bot**"
		if proactive {
			head += " · on its own"
		}
		return head + "\n\n" + answer + "\n\n[Open the " + key + " bot](" + s.cfg.PublicURL + "/assistant/bot/" + key + ")"
	})
}

// nothingToDo says a proactive answer has nothing to report.
func nothingToDo(answer string) bool {
	a := strings.ToLower(strings.Trim(strings.TrimSpace(answer), "*_`.! \n"))
	return len(a) < 80 && strings.HasPrefix(a, "nothing to do")
}

// tgFollow waits for a chat's turn to end and posts what format makes of
// its answer ("" posts nothing), linked so a reply continues that chat.
func (s *Server) tgFollow(tgChat, chatID int64, afterSeq int, typing bool, format func(string) string) {
	s.tgs.mu.Lock()
	if s.tgs.following[chatID] {
		s.tgs.mu.Unlock()
		return
	}
	s.tgs.following[chatID] = true
	s.tgs.mu.Unlock()
	defer func() {
		s.tgs.mu.Lock()
		delete(s.tgs.following, chatID)
		s.tgs.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	tick := time.NewTicker(s.tgFollowEvery)
	defer tick.Stop()
	for i := 0; ; i++ {
		if typing && i%3 == 0 {
			_ = s.tg.Typing(ctx, tgChat)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.assistant.Reconcile(ctx, chatID)
		chat, err := s.store.ChatByID(ctx, chatID)
		if err != nil {
			return
		}
		if chat.Busy {
			continue
		}
		msgs, err := s.store.ChatMessages(ctx, chatID, afterSeq)
		if err != nil {
			return
		}
		answer := ""
		for _, m := range msgs {
			if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
				answer = m.Content
			}
		}
		if answer == "" && chat.LastError != "" {
			answer = "⚠️ " + chat.LastError
		}
		if answer == "" {
			answer = "(no answer)"
		}
		if answer = format(answer); answer != "" {
			s.tgSend(ctx, tgChat, answer, nil, &store.TelegramLink{Kind: "chat", ChatID: &chatID})
		}
		return
	}
}

// ── Notifications ─────────────────────────────────────────────────────────

type promptQuestion struct {
	Question    string `json:"question"`
	Header      string `json:"header"`
	MultiSelect bool   `json:"multiSelect"`
	Options     []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

func promptQuestions(input json.RawMessage) []promptQuestion {
	var in struct {
		Questions []promptQuestion `json:"questions"`
	}
	_ = json.Unmarshal(input, &in)
	return in.Questions
}

// tgNotifyPrompt sends what an interactive run asks, once per prompt.
func (s *Server) tgNotifyPrompt(runID int64, p *store.RunPrompt) {
	s.tgAsync(func(ctx context.Context) {
		set, on := s.tgOn(ctx)
		if !on || !set.Prompts || p.Status != "pending" || s.store.TelegramPromptSent(ctx, p.ID) {
			return
		}
		run, err := s.store.RunByID(ctx, runID)
		if err != nil {
			return
		}
		md, kb := promptMessage(run, p, s.cfg.PublicURL)
		s.tgSend(ctx, set.ChatID, md, kb, &store.TelegramLink{Kind: "prompt", RunID: &run.ID, PromptID: &p.ID})
	})
}

func promptMessage(run *store.Run, p *store.RunPrompt, publicURL string) (string, telegram.Keyboard) {
	head := fmt.Sprintf("Run [#%d](%s/agents/runs/%d) · %s", run.ID, publicURL, run.ID, runWhere(run))
	btn := func(label, decision string) telegram.Button {
		return telegram.Button{Text: label, Data: fmt.Sprintf("p:%d:%s", p.ID, decision)}
	}
	var input map[string]any
	_ = json.Unmarshal(p.Input, &input)
	switch p.Kind {
	case "question":
		qs := promptQuestions(p.Input)
		var b strings.Builder
		fmt.Fprintf(&b, "❓ **Claude asks** — %s\n", head)
		for i, q := range qs {
			if len(qs) > 1 {
				fmt.Fprintf(&b, "\n%d. ", i+1)
			} else {
				b.WriteString("\n")
			}
			b.WriteString("**" + q.Question + "**")
			if q.MultiSelect {
				b.WriteString(" (one or more)")
			}
			b.WriteString("\n")
			for _, o := range q.Options {
				line := "- " + o.Label
				if o.Description != "" {
					line += " — " + o.Description
				}
				b.WriteString(telegram.Clip(line, 300) + "\n")
			}
		}
		var kb telegram.Keyboard
		if len(qs) == 1 && !qs[0].MultiSelect && len(qs[0].Options) > 0 {
			for i, o := range qs[0].Options {
				if i == 8 {
					break
				}
				kb = append(kb, []telegram.Button{btn(telegram.Clip(o.Label, 40), fmt.Sprintf("o%d", i))})
			}
			b.WriteString("\nTap an option, or reply to this message in your own words.")
		} else if len(qs) > 1 {
			b.WriteString("\nReply to this message with one answer per line, in order.")
		} else {
			b.WriteString("\nReply to this message with your answer.")
		}
		kb = append(kb, []telegram.Button{btn("Decline", "deny")})
		return b.String(), kb
	case "plan":
		plan, _ := input["plan"].(string)
		md := fmt.Sprintf("📋 **Plan to approve** — %s\n\n%s\n\nReply to this message to send it back with changes.",
			head, telegram.Clip(plan, 3000))
		return md, telegram.Keyboard{
			{btn("Approve", "approve"), btn("Approve + accept edits", "approve_edits")},
			{btn("Deny", "deny")},
		}
	}
	md := fmt.Sprintf("🔐 **%s** wants permission — %s\n\n%s\nReply to this message to deny with a reason.",
		p.ToolName, head, describeToolInput(p.ToolName, input, p.Description))
	return md, telegram.Keyboard{
		{btn("Allow", "allow"), btn("Allow for session", "allow_always")},
		{btn("Deny", "deny")},
	}
}

// describeToolInput is the part of a tool call worth reading on a phone.
func describeToolInput(tool string, input map[string]any, description string) string {
	str := func(k string) string { v, _ := input[k].(string); return v }
	fence := func(s string) string { return "```\n" + telegram.Clip(s, 1500) + "\n```\n" }
	switch {
	case tool == "Bash":
		out := fence(str("command"))
		if d := str("description"); d != "" {
			out = d + "\n" + out
		}
		return out
	case str("file_path") != "":
		return "`" + str("file_path") + "`\n"
	case str("url") != "":
		return str("url") + "\n"
	case str("pattern") != "":
		return "`" + str("pattern") + "`\n"
	}
	if description != "" {
		return telegram.Clip(description, 500) + "\n"
	}
	raw, _ := json.Marshal(input)
	return fence(string(raw))
}

// tgNotifyAwaiting says an interactive run finished its turn and waits.
func (s *Server) tgNotifyAwaiting(runID int64) {
	s.tgAsync(func(ctx context.Context) {
		set, on := s.tgOn(ctx)
		if !on || !set.Runs {
			return
		}
		run, err := s.store.RunByID(ctx, runID)
		if err != nil || run.ChatTurnID != nil {
			return
		}
		// The machine ships events in batches (every 400 ms): let the turn's
		// last words arrive before quoting them.
		time.Sleep(1500 * time.Millisecond)
		said := s.store.LastRunText(ctx, runID)
		if said == "" {
			said = "(nothing to show)"
		}
		md := fmt.Sprintf("💬 **Run [#%d](%s/agents/runs/%d) waits for you** · %s\n\n%s\n\nReply to this message to continue.",
			run.ID, s.cfg.PublicURL, run.ID, runWhere(run), telegram.Clip(said, 3000))
		kb := telegram.Keyboard{{{Text: "End session", Data: fmt.Sprintf("e:%d", run.ID)}}}
		s.tgSend(ctx, set.ChatID, md, kb, &store.TelegramLink{Kind: "run", RunID: &run.ID})
	})
}

// tgNotifyFinished says a run ended (not the Assistant's own sessions,
// whose answer goes to their chat, and not a cancelled one).
func (s *Server) tgNotifyFinished(runID int64) {
	s.tgAsync(func(ctx context.Context) {
		set, on := s.tgOn(ctx)
		if !on || !set.Runs {
			return
		}
		run, err := s.store.RunByID(ctx, runID)
		if err != nil || run.ChatTurnID != nil || run.Status == "cancelled" {
			return
		}
		body := run.Result
		if run.Status == "failed" && run.Error != "" {
			body = run.Error
		}
		if body == "" {
			body = s.store.LastRunText(ctx, runID)
		}
		what := run.Command
		if run.Kind == "agent" {
			what = telegram.Clip(strings.Join(strings.Fields(run.Prompt), " "), 120)
		}
		md := fmt.Sprintf("%s **Run [#%d](%s/agents/runs/%d) %s** · %s\n_%s_",
			runIcon(run.Status), run.ID, s.cfg.PublicURL, run.ID, run.Status, runWhere(run), what)
		if body != "" {
			md += "\n\n" + telegram.Clip(body, 2500)
		}
		s.tgSend(ctx, set.ChatID, md, nil, &store.TelegramLink{Kind: "run", RunID: &run.ID})
	})
}

// NotifyCheckup posts the scheduled check-up (checkup.Runner calls it).
func (s *Server) NotifyCheckup(ctx context.Context, c *store.Checkup) {
	set, on := s.tgOn(ctx)
	if !on || !set.Checkup {
		return
	}
	s.tgSend(ctx, set.ChatID, checkupMarkdown(c, s.cfg.PublicURL), nil, nil)
}

// ── Settings ──────────────────────────────────────────────────────────────

func (s *Server) telegramStatus(ctx context.Context) map[string]any {
	set, _ := s.store.TelegramSettings(ctx)
	k, _ := s.telegramToken(ctx)
	s.tgs.mu.Lock()
	defer s.tgs.mu.Unlock()
	out := map[string]any{"settings": set, "token_configured": k != "", "bot": s.tgs.bot,
		"last_error": s.tgs.lastError, "last_error_at": nil, "last_poll_at": nil}
	if !s.tgs.lastErrAt.IsZero() {
		out["last_error_at"] = s.tgs.lastErrAt.UTC()
	}
	if !s.tgs.lastOK.IsZero() {
		out["last_poll_at"] = s.tgs.lastOK.UTC()
	}
	return out
}

func (s *Server) getTelegram(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, s.telegramStatus(r.Context()))
}

func (s *Server) updateTelegram(w http.ResponseWriter, r *http.Request, u *store.User) {
	set, err := s.store.TelegramSettings(r.Context())
	if err != nil {
		writeErr(w, r, err)
		return
	}
	var in struct {
		Enabled, Prompts, Runs, Checkup, Assistant *bool
		ChatID                                     *int64 `json:"chat_id"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.ChatID != nil && *in.ChatID != set.ChatID {
		// The chat is where approvals are given: moving it is sensitive.
		if !sessionInfo(r).Elevated() {
			writeError(w, http.StatusForbidden, "elevation_required", "confirm your password to continue")
			return
		}
		set.ChatID = *in.ChatID
	}
	for dst, v := range map[*bool]*bool{&set.Enabled: in.Enabled, &set.Prompts: in.Prompts, &set.Runs: in.Runs,
		&set.Checkup: in.Checkup, &set.Assistant: in.Assistant} {
		if v != nil {
			*dst = *v
		}
	}
	if err := s.store.SetTelegramSettings(r.Context(), set); err != nil {
		writeErr(w, r, err)
		return
	}
	s.sec(r, "telegram_settings", fmt.Sprintf("enabled=%t chat=%d prompts=%t runs=%t checkup=%t assistant=%t",
		set.Enabled, set.ChatID, set.Prompts, set.Runs, set.Checkup, set.Assistant))
	s.getTelegram(w, r, u)
}

// setTelegramToken stores the bot token as the vault item Forge reads.
func (s *Server) setTelegramToken(w http.ResponseWriter, r *http.Request, u *store.User) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if !telegramTokenRe.MatchString(in.Token) {
		writeErr(w, r, &store.ValidationError{Field: "token", Message: "that does not look like a bot token (123456:ABC…)"})
		return
	}
	if err := s.store.SaveIntegrationSecret(r.Context(), s.box, "Telegram bot token", telegramTag, in.Token, s.clientIP(r)); err != nil {
		writeErr(w, r, err)
		return
	}
	s.tgs.mu.Lock()
	s.tgs.bot = ""
	s.tgs.mu.Unlock()
	s.sec(r, "telegram_token", "Telegram bot token replaced")
	s.getTelegram(w, r, u)
}

// testTelegram checks the token and, with a chat set, says hello there.
func (s *Server) testTelegram(w http.ResponseWriter, r *http.Request, u *store.User) {
	writeJSON(w, http.StatusOK, s.TestTelegram(r.Context()))
}

// TestTelegram is the Settings page's (and `forge telegram test`'s) check.
func (s *Server) TestTelegram(ctx context.Context) map[string]any {
	me, err := s.tg.GetMe(ctx)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	s.tgs.mu.Lock()
	s.tgs.bot = me.Username
	s.tgs.mu.Unlock()
	set, _ := s.store.TelegramSettings(ctx)
	if set.ChatID == 0 {
		return map[string]any{"ok": true, "bot": me.Username, "sent": false,
			"note": "token works; send the bot a message to learn your chat id"}
	}
	_, err = s.tg.Send(ctx, telegram.Send{ChatID: set.ChatID, Text: "✅ Forge is connected (" + telegram.Escape(s.cfg.PublicURL) + ").", HTML: true})
	if err != nil {
		return map[string]any{"ok": false, "bot": me.Username, "error": err.Error()}
	}
	return map[string]any{"ok": true, "bot": me.Username, "sent": true}
}

// ── Bots ──────────────────────────────────────────────────────────────────

func (s *Server) tgBots(ctx context.Context) string {
	bots, err := s.store.ListBots(ctx, store.TodayFor("UTC"))
	if err != nil {
		return "Could not list the bots: " + err.Error()
	}
	var b strings.Builder
	for _, bot := range bots {
		state := "on demand"
		switch {
		case !bot.Settings.Enabled:
			state = "off"
		case bot.Settings.Proactive:
			state = "proactive"
		}
		fmt.Fprintf(&b, "- **%s** · %s", bot.Project.Key, state)
		if bot.Last != nil {
			fmt.Fprintf(&b, " — %s", telegram.Clip(strings.Join(strings.Fields(bot.Last.Content), " "), 80))
		}
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "No projects yet."
	}
	return b.String() + "\n/bot PROJECT message talks to one."
}

// tgBot sends a Telegram message to a project's bot; its answer comes back
// here, and a reply to that answer goes on with the same bot.
func (s *Server) tgBot(ctx context.Context, set store.TelegramSettings, key, text string) {
	say := func(md string) { s.tgSend(ctx, set.ChatID, md, nil, nil) }
	if !set.Assistant {
		say("The Assistant (and the bots) are off for Telegram (Settings → Telegram).")
		return
	}
	user, err := s.store.FirstUser(ctx)
	if err != nil {
		say("No account yet.")
		return
	}
	if _, _, err := s.startBotTurn(ctx, user, key, "telegram", text, true); err != nil {
		var ve *store.ValidationError
		switch {
		case errors.Is(err, store.ErrNotFound):
			say("No project " + key + ". /bots lists them.")
		case errors.As(err, &ve):
			say(ve.Message)
		case errors.Is(err, store.ErrConflict):
			say("The " + key + " bot is still working on the last message.")
		default:
			say(tgAssistantErr(err))
		}
		return
	}
	s.tgSecurity(ctx, "telegram_bot_message", key)
}
