package message

import (
	"context"
	"encoding/json"
	"github.com/jdvr/go-again"
	"github.com/rs/zerolog/log"
	"github.com/segmentio/kafka-go"
	"github.com/slack-go/slack"
	"github.com/spf13/viper"
	"sumup-slack-notifier/handler"
	"time"
)

type RetryMessageOperation struct {
	Reader *kafka.Reader
}

func Consume(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	brokers := viper.GetStringSlice("kafka.bootstrap-servers")
	readerConfig := getKafkaConfig(&brokers)
	reader := kafka.NewReader(readerConfig)
	defer func(reader *kafka.Reader) {
		err := reader.Close()
		if err != nil {
			log.Info().Msgf("%s", err.Error())
		}
	}(reader)
	log.Info().Msgf("initialized kafka reader")

	slackHandler := getSlackHandler()

	slackRecoveryWriter := &kafka.Writer{
		Addr:  kafka.TCP(brokers...),
		Topic: viper.GetString("kafka.recovery.topic"),
	}
	defer func(slackRecoveryWriter *kafka.Writer) {
		err := slackRecoveryWriter.Close()
		if err != nil {
			log.Info().Msgf("%s", err.Error())
		}
	}(slackRecoveryWriter)

	readMessages(reader, slackRecoveryWriter, slackHandler, ctx, cancel)
}

func readMessages(reader *kafka.Reader, producer *kafka.Writer,
	slackHandler *handler.SlackHandler, ctx context.Context,
	cancel context.CancelFunc) {
	backoffConfig := getExponentialBackoffConfig()
	retryService := again.WithExponentialBackoff[kafka.Message](backoffConfig)
	retryOperation := RetryMessageOperation{
		reader,
	}

	log.Info().Msgf("Consuming from the main topic...")
	for {
		select {
		case <-ctx.Done():
			log.Info().Msgf("Consumer has been canceled.")
			err := reader.Close()
			if err != nil {
				log.Info().Msgf("%s", err.Error())
			}
			err = producer.Close()
			if err != nil {
				log.Info().Msgf("%s", err.Error())
			}
			return
		default:
		}

		msg, err := retryService.Retry(context.Background(), &retryOperation)
		if err != nil {
			log.Info().Msg("Reading has been unsuccessful, shutting down...")
			cancel()
			continue
		}

		notification, parsingError := slackHandler.ParseData(msg.Value)
		if parsingError != nil {
			log.Error().Msgf("Error parsing message: %v\n", err)
			continue
		}

		recoveryError := slackHandler.SendNotification(notification)
		if isRecoverable(recoveryError) {
			recoveryFallback(ctx, notification, producer, &backoffConfig)
		}
	}
}

func getKafkaConfig(brokers *[]string) kafka.ReaderConfig {
	return kafka.ReaderConfig{
		Brokers:       *brokers,
		Topic:         viper.GetString("kafka.topic"),
		GroupID:       viper.GetString("kafka.group-id"),
		StartOffset:   kafka.LastOffset,
		RetentionTime: time.Duration(viper.GetInt("kafka.retention-hours")) * time.Hour,
	}
}

func getExponentialBackoffConfig() again.BackoffConfiguration {
	initialInterval := viper.GetInt("kafka.retry.initial-interval")
	maxInterval := viper.GetInt("kafka.retry.max-interval")
	multiplier := viper.GetFloat64("kafka.retry.multiplier-interval")
	timeout := viper.GetInt("kafka.retry.timeout")

	return again.BackoffConfiguration{
		InitialInterval:    time.Duration(initialInterval) * time.Second,
		MaxInterval:        time.Duration(maxInterval) * time.Second,
		IntervalMultiplier: multiplier,
		Timeout:            time.Duration(timeout) * time.Second,
	}
}

func getSlackHandler() *handler.SlackHandler {
	return handler.NewSlackHandler(
		slack.New(viper.GetString("SUMUP_SLACK_TOKEN")),
	)
}

func isRecoverable(recoveryError *handler.Error) bool {
	return recoveryError != nil && handler.RECOVERABLE == recoveryError.ErrorType
}

func recoveryFallback(ctx context.Context, notification *handler.SlackNotification,
	producer *kafka.Writer, config *again.BackoffConfiguration) {
	jsonData, _ := json.Marshal(notification)
	Produce(ctx, &jsonData, producer, config)
}

func (operation *RetryMessageOperation) Run(context context.Context) (kafka.Message, error) {
	msg, err := operation.Reader.ReadMessage(context)
	if err != nil {
		log.Info().Msgf("Error reading message: %v\n", err)
		return kafka.Message{}, err
	}

	return msg, nil
}
