// Package bot is the Telegram side: it parses commands and buttons and talks to the timer service.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	tg "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/vionarion/telegram-timer-bot/internal/duration"
	"github.com/vionarion/telegram-timer-bot/internal/store"
	"github.com/vionarion/telegram-timer-bot/internal/timers"
)

const help = `⏱ Бот-таймер

/timer 25 — таймер на 25 минут
/timer 1h30m Созвон — с подписью
/timer 1ч 30м, /timer 45с — по-русски тоже можно
/timers — активные таймеры с кнопкой отмены
/preset чай 3м — сохранить набор
/presets — наборы кнопками: нажал и таймер запущен
/delpreset чай — удалить набор

Таймеры сохраняются и переживают перезапуск бота.`

const maxLabel = 64

type Bot struct {
	api    *tg.Bot
	timers *timers.Service
	store  *store.Store
	loc    *time.Location
}

// New creates the Telegram client; the timer service is attached later with SetService
// because the service needs the bot as its notifier.
func New(token string, st *store.Store, loc *time.Location) (*Bot, error) {
	b := &Bot{store: st, loc: loc}
	api, err := tg.New(token, tg.WithDefaultHandler(b.onUpdate))
	if err != nil {
		return nil, err
	}
	b.api = api
	return b, nil
}

func (b *Bot) SetService(s *timers.Service) { b.timers = s }

// Start receives updates until ctx is cancelled.
func (b *Bot) Start(ctx context.Context) {
	b.api.Start(ctx)
}

// TimerFired implements timers.Notifier.
func (b *Bot) TimerFired(ctx context.Context, t store.Timer, late time.Duration) {
	text := "⏰ Время вышло!"
	if t.Label != "" {
		text = "⏰ " + t.Label + " — время вышло!"
	}
	if late > 0 {
		text += fmt.Sprintf("\n(с опозданием на %s: бот был недоступен)", duration.Format(late))
	}
	b.send(ctx, t.ChatID, text, nil)
}

func (b *Bot) onUpdate(ctx context.Context, _ *tg.Bot, u *models.Update) {
	switch {
	case u.CallbackQuery != nil:
		b.onButton(ctx, u.CallbackQuery)
	case u.Message != nil && strings.HasPrefix(u.Message.Text, "/"):
		b.onCommand(ctx, u.Message)
	}
}

func (b *Bot) onCommand(ctx context.Context, m *models.Message) {
	chatID := m.Chat.ID
	cmd, args, _ := strings.Cut(strings.TrimSpace(m.Text), " ")
	cmd, _, _ = strings.Cut(cmd, "@") // "/timer@my_bot" in groups
	args = strings.TrimSpace(args)

	switch cmd {
	case "/start", "/help":
		b.send(ctx, chatID, help, nil)
	case "/timer", "/t":
		b.cmdTimer(ctx, chatID, args)
	case "/timers":
		b.cmdList(ctx, chatID)
	case "/preset":
		b.cmdPreset(ctx, chatID, args)
	case "/presets":
		b.cmdPresets(ctx, chatID)
	case "/delpreset":
		b.cmdDelPreset(ctx, chatID, args)
	default:
		b.send(ctx, chatID, "Не знаю такой команды. /help — список команд.", nil)
	}
}

// splitLength separates the length from an optional label: "1h 30m Созвон" -> "1h 30m", "Созвон".
// It takes the longest prefix of words that still parses as a duration.
func splitLength(args string) (time.Duration, string, error) {
	words := strings.Fields(args)
	if len(words) == 0 {
		return 0, "", duration.ErrEmpty
	}
	var firstErr error
	for i := len(words); i > 0; i-- {
		d, err := duration.Parse(strings.Join(words[:i], " "))
		if err == nil {
			return d, strings.Join(words[i:], " "), nil
		}
		if i == 1 {
			firstErr = err
		}
	}
	return 0, "", firstErr
}

func (b *Bot) cmdTimer(ctx context.Context, chatID int64, args string) {
	d, label, err := splitLength(args)
	if err != nil {
		b.send(ctx, chatID, "❗ "+err.Error(), nil)
		return
	}
	b.startTimer(ctx, chatID, d, label)
}

func (b *Bot) startTimer(ctx context.Context, chatID int64, d time.Duration, label string) {
	if r := []rune(label); len(r) > maxLabel {
		label = string(r[:maxLabel])
	}
	t, err := b.timers.Start(ctx, chatID, d, label)
	if err != nil {
		b.fail(ctx, chatID, err)
		return
	}
	text := fmt.Sprintf("✅ Таймер на %s, сработает в %s", duration.Format(d), b.clock(t.FireAt))
	if label != "" {
		text += ": " + label
	}
	b.send(ctx, chatID, text, cancelButton(t.ID))
}

func (b *Bot) cmdList(ctx context.Context, chatID int64) {
	list, err := b.timers.List(ctx, chatID)
	if err != nil {
		b.fail(ctx, chatID, err)
		return
	}
	if len(list) == 0 {
		b.send(ctx, chatID, "Активных таймеров нет.", nil)
		return
	}
	var sb strings.Builder
	var rows [][]models.InlineKeyboardButton
	for i, t := range list {
		left := time.Until(t.FireAt)
		name := t.Label
		if name == "" {
			name = "без подписи"
		}
		fmt.Fprintf(&sb, "%d. %s — через %s (в %s)\n", i+1, name, duration.Format(left), b.clock(t.FireAt))
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: fmt.Sprintf("✖ Отменить %d", i+1), CallbackData: "cancel:" + strconv.FormatInt(t.ID, 10),
		}})
	}
	b.send(ctx, chatID, sb.String(), &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (b *Bot) cmdPreset(ctx context.Context, chatID int64, args string) {
	name, rest, _ := strings.Cut(args, " ")
	if name == "" || rest == "" {
		b.send(ctx, chatID, "Формат: /preset название длительность, например /preset чай 3м", nil)
		return
	}
	if len([]rune(name)) > 32 {
		b.send(ctx, chatID, "❗ Название до 32 символов", nil)
		return
	}
	d, err := duration.Parse(rest)
	if err != nil {
		b.send(ctx, chatID, "❗ "+err.Error(), nil)
		return
	}
	presets, err := b.store.ListPresets(ctx, chatID)
	if err != nil {
		b.fail(ctx, chatID, err)
		return
	}
	if len(presets) >= 20 {
		b.send(ctx, chatID, "❗ Не больше 20 наборов, удалите лишние через /delpreset", nil)
		return
	}
	if err := b.store.SavePreset(ctx, store.Preset{ChatID: chatID, Name: name, Length: d}); err != nil {
		b.fail(ctx, chatID, err)
		return
	}
	b.send(ctx, chatID, fmt.Sprintf("💾 Набор «%s» на %s сохранён. /presets — запустить", name, duration.Format(d)), nil)
}

func (b *Bot) cmdPresets(ctx context.Context, chatID int64) {
	presets, err := b.store.ListPresets(ctx, chatID)
	if err != nil {
		b.fail(ctx, chatID, err)
		return
	}
	if len(presets) == 0 {
		b.send(ctx, chatID, "Наборов пока нет. Добавьте: /preset чай 3м", nil)
		return
	}
	var rows [][]models.InlineKeyboardButton
	for _, p := range presets {
		rows = append(rows, []models.InlineKeyboardButton{{
			Text: fmt.Sprintf("▶ %s · %s", p.Name, duration.Format(p.Length)), CallbackData: "preset:" + p.Name,
		}})
	}
	b.send(ctx, chatID, "Ваши наборы:", &models.InlineKeyboardMarkup{InlineKeyboard: rows})
}

func (b *Bot) cmdDelPreset(ctx context.Context, chatID int64, name string) {
	if name == "" {
		b.send(ctx, chatID, "Формат: /delpreset название", nil)
		return
	}
	if err := b.store.DeletePreset(ctx, chatID, name); err != nil {
		b.fail(ctx, chatID, err)
		return
	}
	b.send(ctx, chatID, "🗑 Набор «"+name+"» удалён", nil)
}

func (b *Bot) onButton(ctx context.Context, q *models.CallbackQuery) {
	answer := ""
	defer func() {
		if _, err := b.api.AnswerCallbackQuery(ctx, &tg.AnswerCallbackQueryParams{CallbackQueryID: q.ID, Text: answer}); err != nil {
			slog.Warn("answer callback", "err", err)
		}
	}()
	if q.Message.Message == nil {
		return // message is too old, Telegram doesn't give its chat
	}
	chatID := q.Message.Message.Chat.ID
	kind, value, _ := strings.Cut(q.Data, ":")

	switch kind {
	case "cancel":
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return
		}
		if err := b.timers.Cancel(ctx, chatID, id); err != nil {
			answer = "Этот таймер уже сработал или отменён"
			return
		}
		answer = "Таймер отменён"
		b.send(ctx, chatID, "🛑 Таймер отменён", nil)
	case "preset":
		p, err := b.store.GetPreset(ctx, chatID, value)
		if err != nil {
			answer = "Набор не найден"
			return
		}
		b.startTimer(ctx, chatID, p.Length, p.Name)
	}
}

func cancelButton(id int64) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: "✖ Отменить", CallbackData: "cancel:" + strconv.FormatInt(id, 10)},
	}}}
}

func (b *Bot) clock(t time.Time) string {
	t = t.In(b.loc)
	if y, m, d := t.Date(); time.Now().In(b.loc).Day() != d || time.Now().In(b.loc).Month() != m || time.Now().In(b.loc).Year() != y {
		return t.Format("02.01 15:04:05")
	}
	return t.Format("15:04:05")
}

// fail shows a user-friendly message and logs unexpected errors.
func (b *Bot) fail(ctx context.Context, chatID int64, err error) {
	switch {
	case errors.Is(err, timers.ErrTooMany):
		b.send(ctx, chatID, "❗ "+err.Error(), nil)
	case errors.Is(err, store.ErrNotFound):
		b.send(ctx, chatID, "❗ Не найдено", nil)
	default:
		slog.Error("request failed", "chat", chatID, "err", err)
		b.send(ctx, chatID, "❗ Что-то пошло не так, попробуйте ещё раз", nil)
	}
}

func (b *Bot) send(ctx context.Context, chatID int64, text string, markup models.ReplyMarkup) {
	p := &tg.SendMessageParams{ChatID: chatID, Text: text}
	if markup != nil {
		p.ReplyMarkup = markup
	}
	if _, err := b.api.SendMessage(ctx, p); err != nil {
		slog.Error("send message", "chat", chatID, "err", err)
	}
}
