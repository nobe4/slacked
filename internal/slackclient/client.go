package slackclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/rneatherway/slack"

	"nhooyr.io/websocket"
)

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

type SendMessage struct {
	ThreadTS    string       `json:"thread_ts,omitempty"`
	Channel     string       `json:"channel"` // required
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

type SendMessageResponse struct {
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Warning string  `json:"warning,omitempty"`
	TS      string  `json:"ts,omitempty"`
	Message Message `json:"message,omitempty"`
}

type RTMConnectResponse struct {
	Ok    bool   `json:"ok"`
	Error string `json:"error"`
	URL   string `json:"url"`
}

type BotProfile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (r *SendMessageResponse) Output(team, channelID string) string {
	if !r.OK {
		return fmt.Sprintf("Error: %s", r.Error)
	}
	return fmt.Sprintf("Message permalink https://%s.slack.com/archives/%s/p%s", team, channelID, strings.ReplaceAll(r.TS, ".", ""))
}

type HistoryResponse struct {
	CursorResponseMetadata
	Ok       bool
	HasMore  bool `json:"has_more"`
	Messages []Message
}

type Channel struct {
	ID         string
	Name       string
	Is_Channel bool
}

type ChannelInfoResponse struct {
	Ok      bool
	Channel Channel
}

type ConversationsResponse struct {
	CursorResponseMetadata
	Ok       bool
	Channels []Channel
}

type User struct {
	ID   string
	Name string
}

type UsersResponse struct {
	Ok      bool
	Members []User
}

type UsersInfoResponse struct {
	Ok   bool
	User User
}

type Cache struct {
	Channels map[string]string
	Users    map[string]string
}

type SlackClient struct {
	cachePath string
	team      string
	cache     Cache
	client    *slack.Client
	log       *log.Logger
	tz        *time.Location
}

func New(team string, log *log.Logger) (*SlackClient, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dataHome = path.Join(home, ".local", "share")
	}
	cachePath := path.Join(dataHome, "gh-slack")

	client := slack.NewClient(team)
	err := client.WithCookieAuth()
	if err != nil {
		return nil, err
	}

	c := &SlackClient{
		cachePath: cachePath,
		team:      team,
		client:    client,
		log:       log,
		tz:        time.Now().Location(),
	}

	return c, c.loadCache()
}

// Null produces a SlackClient suitable for testing that does not try to load
// the Slack token or cookies from disk, and starts with an empty cache.
func Null(team string, roundTripper http.RoundTripper) (*SlackClient, error) {
	cacheFile, err := os.CreateTemp("", "gh-slack-cache")
	if err != nil {
		return nil, err
	}

	client := slack.NewClient("test-team")
	client.WithHTTPClient(&http.Client{Transport: roundTripper})

	return &SlackClient{
		team:      team,
		client:    client,
		cachePath: cacheFile.Name(),
		tz:        time.UTC,
	}, nil
}

func (c *SlackClient) UsernameForMessage(message Message) (string, error) {
	if message.User != "" {
		return c.UsernameForID(message.User)
	}
	if message.BotID != "" {
		return fmt.Sprintf("bot %s", message.BotID), nil
	}
	return "ghost", nil
}

func (c *SlackClient) API(verb, path string, params map[string]string, body []byte) ([]byte, error) {
	return c.client.API(context.TODO(), verb, path, params, body)
}

func (c *SlackClient) get(path string, params map[string]string) ([]byte, error) {
	return c.API("GET", path, params, []byte("{}"))
}

func (c *SlackClient) post(path string, params map[string]string, msg *SendMessage) ([]byte, error) {
	messageBytes, err := json.Marshal(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal message: %w", err)
	}

	return c.API("POST", path, params, messageBytes)
}

func (c *SlackClient) ChannelInfo(id string) (*Channel, error) {
	body, err := c.get("conversations.info",
		map[string]string{"channel": id})
	if err != nil {
		return nil, err
	}

	channelInfoReponse := &ChannelInfoResponse{}
	err = json.Unmarshal(body, channelInfoReponse)
	if err != nil {
		return nil, err
	}

	if !channelInfoReponse.Ok {
		return nil, fmt.Errorf("conversations.info response not OK: %s", body)
	}

	return &channelInfoReponse.Channel, nil
}

func (c *SlackClient) conversations() ([]Channel, error) {
	fmt.Fprintf(os.Stderr, "Populating channel cache (this may take a while)...")

	channels := make([]Channel, 0, 1000)
	conversations := &ConversationsResponse{}
	for {
		c.log.Printf("Fetching conversations with cursor %q", conversations.ResponseMetadata.NextCursor)
		body, err := c.get("conversations.list",
			map[string]string{
				"cursor":           conversations.ResponseMetadata.NextCursor,
				"exclude_archived": "true",
				"limit":            "1000",

				// TODO: we might want to support DMs in the future
				"types": "public_channel, private_channel",
			},
		)
		if err != nil {
			return nil, err
		}

		if err = json.Unmarshal(body, conversations); err != nil {
			return nil, err
		}

		if !conversations.Ok {
			return nil, fmt.Errorf("conversations response not OK: %s", body)
		}

		channels = append(channels, conversations.Channels...)
		fmt.Fprintf(os.Stderr, "%d...", len(channels))

		if conversations.ResponseMetadata.NextCursor == "" {
			break
		}
	}

	fmt.Fprintf(os.Stderr, "done!\n")
	return channels, nil
}

func (c *SlackClient) users(params map[string]string) (*UsersResponse, error) {
	body, err := c.get("users.list", nil)
	if err != nil {
		return nil, err
	}

	users := &UsersResponse{}
	err = json.Unmarshal(body, users)
	if err != nil {
		return nil, err
	}

	if !users.Ok {
		return nil, fmt.Errorf("users response not OK: %s", body)
	}

	return users, nil
}

func (c *SlackClient) loadCache() error {
	content, err := os.ReadFile(c.cachePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	return json.Unmarshal(content, &c.cache)
}

func (c *SlackClient) History(channelID string, startTimestamp string, thread string, limit int) (*HistoryResponse, error) {
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

	body, err := c.get("conversations.replies", params)
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
	body, err = c.get("conversations.history",
		map[string]string{
			"channel":   channelID,
			"oldest":    startTimestamp,
			"inclusive": "true",
			"limit":     strconv.Itoa(limit)})
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
	c.log.Println(string(body))
	c.log.Printf("%#v", historyResponse)
	return historyResponse, nil
}

func (c *SlackClient) saveCache() error {
	bs, err := json.Marshal(c.cache)
	if err != nil {
		return err
	}

	err = os.MkdirAll(path.Dir(c.cachePath), 0755)
	if err != nil {
		return err
	}

	err = os.WriteFile(c.cachePath, bs, 0644)
	if err != nil {
		return err
	}

	return nil
}

func (c *SlackClient) UsernameForID(id string) (string, error) {
	if name, ok := c.cache.Users[id]; ok {
		return name, nil
	}

	ur, err := c.users(nil)
	if err != nil {
		return "", err
	}

	c.cache.Users = make(map[string]string)
	for _, ch := range ur.Members {
		c.cache.Users[ch.ID] = ch.Name
	}

	err = c.saveCache()
	if err != nil {
		return "", err
	}

	if name, ok := c.cache.Users[id]; ok {
		return name, nil
	}

	body, err := c.get("users.info", map[string]string{"user": id})
	if err != nil {
		return "", fmt.Errorf("no user with id %q: %w", id, err)
	}

	user := &UsersInfoResponse{}
	err = json.Unmarshal(body, user)
	if err != nil {
		return "", err
	}

	if !user.Ok {
		return "", errors.New("users.info response not OK")
	}

	c.cache.Users[id] = user.User.Name
	err = c.saveCache()
	if err != nil {
		return "", err
	}

	return user.User.Name, nil
}

func (c *SlackClient) ChannelIDForName(name string) (string, error) {
	if id, ok := c.cache.Channels[name]; ok {
		return id, nil
	}

	channels, err := c.conversations()
	if err != nil {
		return "", err
	}

	c.cache.Channels = make(map[string]string)
	for _, ch := range channels {
		if !ch.Is_Channel {
			fmt.Fprintf(os.Stderr, "Skipping non-channel %q\n", ch.Name)
			continue
		}
		c.cache.Channels[ch.Name] = ch.ID
	}

	err = c.saveCache()
	if err != nil {
		return "", err
	}

	if id, ok := c.cache.Channels[name]; ok {
		return id, nil
	}

	return "", fmt.Errorf("could not find any channel with name %q", name)
}

func (c *SlackClient) GetLocation() *time.Location {
	return c.tz
}

func (c *SlackClient) SendMessage(channelID string, message string) (*SendMessageResponse, error) {
	body, err := c.post("chat.postMessage",
		map[string]string{}, &SendMessage{
			Channel: channelID,
			Text:    message,
		})
	if err != nil {
		return nil, err
	}

	response := &SendMessageResponse{}
	err = json.Unmarshal(body, response)
	if err != nil {
		return nil, err
	}

	if !response.OK {
		return nil, fmt.Errorf("chat.postMessage response not OK: %s", body)
	}

	return response, nil
}

func (c *SlackClient) ConnectToRTM() (*RTMClient, error) {
	response, err := c.get("rtm.connect", nil)
	if err != nil {
		return nil, err
	}

	// This is a Tier 1 Slack API, which are allowed to call once a minute with
	// some bursts. It would be nice to cache the URL result in case we need to
	// reconnect quickly (for example if gh-slack is called in a loop by some
	// external program). Although the URL is valid for 30 seconds, it seems
	// that it can only be used once, so that isn't possible.
	connectResponse := &RTMConnectResponse{}
	err = json.Unmarshal(response, connectResponse)
	if err != nil {
		return nil, err
	}

	if !connectResponse.Ok {
		return nil, fmt.Errorf("rtm.connect response not OK: %s", response)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	socketConnection, _, err := websocket.Dial(ctx, connectResponse.URL, &websocket.DialOptions{})
	if err != nil {
		return nil, err
	}

	return &RTMClient{
		conn:        socketConnection,
		slackClient: c,
	}, err
}
