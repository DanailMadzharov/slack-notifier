package handler

import (
	"encoding/json"
	"github.com/rs/zerolog/log"
	"github.com/slack-go/slack"
)

type SlackHandler struct {
	SlackClient *slack.Client
}

type SlackNotification struct {
	SlackChannel string `json:"slackChannel" validate:"required,lt=200"`
	Message      string `json:"message" validate:"required,lt=200"`
}

func NewSlackHandler(client *slack.Client) *SlackHandler {
	return &SlackHandler{
		SlackClient: client,
	}
}

func (s *SlackHandler) ParseData(rawData []byte) (*SlackNotification, *Error) {
	var slackData SlackNotification
	err := json.Unmarshal(rawData, &slackData)
	if err != nil {
		return nil, &Error{
			ErrorType: NON_RECOVERABLE,
		}
	}

	return &slackData, nil
}

func (s *SlackHandler) SendNotification(notification *SlackNotification) *Error {
	blocks := getSlackBlocks(notification.Message)
	channelID, timestamp, err :=
		s.SlackClient.PostMessage(notification.SlackChannel, slack.MsgOptionBlocks(blocks...))
	if err != nil {
		return &Error{
			ErrorType: RECOVERABLE,
		}
	}
	log.Info().Msgf("Send slack message successfully. Channel: %s, timestamp: %s", channelID, timestamp)

	return nil
}

func getSlackBlocks(message string) []slack.Block {
	return []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, message, false, false),
			nil,
			nil,
		),
		slack.NewDividerBlock(),
		slack.NewContextBlock(
			"",
			slack.NewTextBlockObject(slack.MarkdownType, "This is a notification message.", false, false),
		),
	}
}
