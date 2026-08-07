// Package converse implements the provider-neutral Amazon Bedrock Converse API.
package converse

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	basecfg "github.com/viant/agently-core/genai/llm/provider/base"
	authaws "github.com/viant/scy/auth/aws"
	"github.com/viant/scy/cred/secret"
)

type runtimeClient interface {
	Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// Client is a model-independent Amazon Bedrock Converse client.
type Client struct {
	BedrockClient  runtimeClient
	Model          string
	Region         string
	CredentialsURL string
	MaxTokens      int
	Temperature    *float64
	UsageListener  basecfg.UsageListener
	secrets        *secret.Service
}

// ClientOption configures a Client.
type ClientOption func(*Client)

func WithRegion(region string) ClientOption      { return func(c *Client) { c.Region = region } }
func WithCredentialsURL(url string) ClientOption { return func(c *Client) { c.CredentialsURL = url } }
func WithMaxTokens(max int) ClientOption         { return func(c *Client) { c.MaxTokens = max } }
func WithTemperature(value float64) ClientOption { return func(c *Client) { c.Temperature = &value } }
func WithUsageListener(listener basecfg.UsageListener) ClientOption {
	return func(c *Client) { c.UsageListener = listener }
}
func WithRuntimeClient(client runtimeClient) ClientOption {
	return func(c *Client) { c.BedrockClient = client }
}

// NewClient creates a provider-neutral Amazon Bedrock client.
func NewClient(ctx context.Context, model string, options ...ClientOption) (*Client, error) {
	c := &Client{Model: model, secrets: secret.New()}
	for _, option := range options {
		option(c)
	}
	if c.BedrockClient != nil {
		return c, nil
	}
	var cfg aws.Config
	var err error
	if c.CredentialsURL != "" {
		generic, loadErr := c.secrets.GetCredentials(ctx, c.CredentialsURL)
		if loadErr != nil {
			return nil, loadErr
		}
		awsCfg, configErr := authaws.NewConfig(ctx, &generic.Aws)
		if configErr != nil {
			return nil, configErr
		}
		cfg = *awsCfg
	} else {
		cfg, err = config.LoadDefaultConfig(ctx, config.WithRegion(c.Region))
		if err != nil {
			return nil, err
		}
	}
	if c.Region != "" {
		cfg.Region = c.Region
	}
	c.BedrockClient = bedrockruntime.NewFromConfig(cfg)
	return c, nil
}
