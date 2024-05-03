package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/rneatherway/slack"
)

func main() {
	a := app.New()
	w := a.NewWindow("Hello")

	input := widget.NewEntry()
	input.SetPlaceHolder("slack url")

	output := widget.NewTextGrid()

	w.SetContent(container.NewVBox(
		input,
		widget.NewButton("Archive", func() {
			h, err := readSlack(input.Text)
			if err != nil {
				panic(err)
			}
			output.SetText(h)
		}),
		widget.NewSeparator(),
		output,
		widget.NewButton("Copy", func() {
			w.Clipboard().SetContent(output.Text())
		}),
	))

	w.ShowAndRun()
}

type UserProvider struct{}

func (u UserProvider) UsernameForID(id string) (string, error) {
	return id, nil
}

func readSlack(link string) (string, error) {
	linkParts, err := parsePermalink(link)
	if err != nil {
		return "", err
	}

	client := slack.NewClient(linkParts.team)
	if err := client.WithCookieAuth(); err != nil {
		return "", err
	}

	// TODO: 10should be an input
	history, err := history(client, linkParts.channelID, linkParts.timestamp, linkParts.thread, 10)
	if err != nil {
		return "", err
	}
	userProvider := UserProvider{}

	output, err := FromMessages(userProvider, *history)
	if err != nil {
		return "", err
	}
	return output, nil
}

type linkParts struct {
	team      string
	channelID string
	timestamp string
	thread    string
}

func parsePermalink(link string) (linkParts, error) {
	u, err := url.Parse(link)
	if err != nil {
		return linkParts{}, err
	}

	team, ok := strings.CutSuffix(u.Hostname(), ".slack.com")
	if !ok {
		return linkParts{}, fmt.Errorf("expected slack.com subdomain: %q", link)
	}

	pathSegments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(pathSegments) != 3 || pathSegments[0] != "archives" {
		return linkParts{}, fmt.Errorf("expected path of the form /archives/<channel>/p<timestamp>: %q", link)
	}

	channel := pathSegments[1]
	timestamp := pathSegments[2][1:len(pathSegments[2])-6] + "." + pathSegments[2][len(pathSegments[2])-6:]

	return linkParts{
		team:      team,
		channelID: channel,
		timestamp: timestamp,
		thread:    u.Query().Get("thread_ts"),
	}, nil
}

type Cursor struct {
	NextCursor string `json:"next_cursor"`
}

type CursorResponseMetadata struct {
	ResponseMetadata Cursor `json:"response_metadata"`
}

type Attachment struct {
	ID   int
	Text string
}

type Message struct {
	User        string
	BotID       string `json:"bot_id"`
	Text        string
	Attachments []Attachment
	Ts          string
	Type        string
	ReplyCount  int `json:"reply_count"`
}

type HistoryResponse struct {
	CursorResponseMetadata
	Ok       bool
	HasMore  bool `json:"has_more"`
	Messages []Message
}

func history(c *slack.Client, channelID string, startTimestamp string, thread string, limit int) (*HistoryResponse, error) {
	params := map[string]string{
		"channel":   channelID,
		"ts":        startTimestamp,
		"inclusive": "true",
		"limit":     strconv.Itoa(limit),
	}

	if thread != "" {
		params["ts"] = thread
		params["oldest"] = startTimestamp
	}

	body, err := c.API(context.TODO(), "GET", "conversations.replies", params, nil)
	if err != nil {
		return nil, err
	}

	historyResponse := &HistoryResponse{}
	err = json.Unmarshal(body, historyResponse)
	if err != nil {
		return nil, err
	}

	if !historyResponse.Ok {
		return nil, fmt.Errorf("conversations.replies response not OK: %s", body)
	}

	// If thread was specified, then we are fetching only part of a thread and
	// should remove the first message if it has a reply count as we don't want
	// the root message.
	if thread != "" && historyResponse.Messages[0].ReplyCount != 0 && len(historyResponse.Messages) > 1 {
		historyResponse.Messages = historyResponse.Messages[1:]
	}

	if thread != "" || historyResponse.Messages[0].ReplyCount != 0 {
		// Either we are deliberately fetching a subthread, or an entire thread.
		return historyResponse, nil
	}

	// Otherwise we read the general channel history
	body, err = c.API(context.TODO(), "GET", "conversations.history",
		map[string]string{
			"channel":   channelID,
			"oldest":    startTimestamp,
			"inclusive": "true",
			"limit":     strconv.Itoa(limit)}, nil)
	if err != nil {
		return nil, err
	}

	err = json.Unmarshal(body, historyResponse)
	if err != nil {
		return nil, err
	}

	if !historyResponse.Ok {
		return nil, fmt.Errorf("conversations.history response not OK: %s", body)
	}

	return historyResponse, nil
}

var userRE = regexp.MustCompile("<@[A-Z0-9]+>")
var linkRE = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)
var openCodefence = regexp.MustCompile("(?m)^```")
var closeCodefence = regexp.MustCompile("(?m)(.)```$")

func interpolateUsers(u UserProvider, s string) (string, error) {
	userLocations := userRE.FindAllStringIndex(s, -1)
	out := &strings.Builder{}
	last := 0
	for _, userLocation := range userLocations {
		start := userLocation[0]
		end := userLocation[1]

		username, err := u.UsernameForID(s[start+2 : end-1])
		if err != nil {
			return "", err
		}
		out.WriteString(s[last:start])
		out.WriteString("`@")
		out.WriteString(username)
		out.WriteRune('`')
		last = end
	}
	out.WriteString(s[last:])

	return out.String(), nil
}

func parseUnixTimestamp(s string) (*time.Time, error) {
	tsParts := strings.Split(s, ".")
	if len(tsParts) != 2 {
		return nil, fmt.Errorf("timestamp '%s' is not in <seconds>.<milliseconds> format", s)
	}

	seconds, err := strconv.ParseInt(tsParts[0], 10, 64)
	if err != nil {
		return nil, err
	}

	nanos, err := strconv.ParseInt(tsParts[1], 10, 64)
	if err != nil {
		return nil, err
	}

	result := time.Unix(seconds, nanos)
	return &result, nil
}

func convert(u UserProvider, b *strings.Builder, s string) error {
	text, err := interpolateUsers(u, s)
	if err != nil {
		return err
	}

	text = linkRE.ReplaceAllString(text, "[$2]($1)")
	text = openCodefence.ReplaceAllString(text, "```\n")
	text = closeCodefence.ReplaceAllString(text, "$1\n```")

	for _, line := range strings.Split(text, "\n") {
		// TODO: Might be a good idea to escape 'line'
		fmt.Fprintf(b, "> %s\n", line)
	}

	return nil
}

func FromMessages(u UserProvider, history HistoryResponse) (string, error) {
	b := &strings.Builder{}
	messages := history.Messages
	msgTimes := make(map[string]time.Time, len(messages))

	for _, message := range messages {
		tm, err := parseUnixTimestamp(message.Ts)
		if err != nil {
			return "", err
		}

		msgTimes[message.Ts] = *tm
	}

	// It's surprising that these messages are not already always returned in date order,
	// and actually I observed initially that they seemed to be, but at least some of the
	// time they are returned in reverse order so it's simpler to just sort them now.
	sort.Slice(messages, func(i, j int) bool {
		return msgTimes[messages[i].Ts].Before(msgTimes[messages[j].Ts])
	})

	lastSpeakerID := ""

	for i, message := range messages {
		username := "ghost"
		if message.User != "" {
			username = message.User
		}
		if message.BotID != "" {
			username = fmt.Sprintf("bot %s", message.BotID)
		}

		speakerID := message.User
		if speakerID == "" {
			speakerID = message.BotID
		}

		messageTime := msgTimes[message.Ts]
		messageTimeDiffInMinutes := 0

		// How far apart in minutes can two messages be, by the same author, before we repeat the header line?
		messageTimeMinuteCutoff := 60

		if i > 0 {
			prevMessage := messages[i-1]
			prevMessageTime := msgTimes[prevMessage.Ts]
			messageTimeDiffInMinutes = int(messageTime.Sub(prevMessageTime).Minutes())
		}

		if lastSpeakerID != "" && speakerID != lastSpeakerID || messageTimeDiffInMinutes > messageTimeMinuteCutoff {
			fmt.Fprintf(b, "\n")
		}

		includeSpeakerHeader := lastSpeakerID == "" || speakerID != lastSpeakerID ||
			messageTimeDiffInMinutes > messageTimeMinuteCutoff

		if includeSpeakerHeader {
			fmt.Fprintf(b, "> **%s** at %s\n",
				username,
				messageTime.In(time.Now().Location()).Format("2006-01-02 15:04 MST"))
		}
		fmt.Fprintf(b, ">\n")

		if message.Text != "" {
			if err := convert(u, b, message.Text); err != nil {
				return "", err
			}
		}

		// These seem to be mostly bot messages so far. Perhaps we should just skip them?
		for _, a := range message.Attachments {
			if err := convert(u, b, a.Text); err != nil {
				return "", err
			}
		}

		if !includeSpeakerHeader {
			b.WriteString("\n")
		}

		lastSpeakerID = speakerID
	}

	return b.String(), nil
}

func WrapInDetails(channelName, link, s string) string {
	return fmt.Sprintf("Slack conversation archive of [`#%s`](%s)\n\n<details>\n  <summary>Click to expand</summary>\n\n%s\n</details>",
		channelName, link, s)
}
