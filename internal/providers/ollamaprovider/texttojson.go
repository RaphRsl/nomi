package ollamaprovider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/nullswan/nomi/internal/chat"
	"github.com/nullswan/nomi/internal/completion"
	baseprovider "github.com/nullswan/nomi/internal/providers/base"
	"github.com/ollama/ollama/api"
)

const (
	OLamaTextToJSONDefaultModel     = "llama3.1:latest"
	OLamaTextToJSONDefaultModelFast = "llama3.2:latest"
)

type TextToJSONProvider struct {
	config olamaProviderConfig
	client *api.Client

	cmd *exec.Cmd
}

func NewTextToJSONProvider(
	config olamaProviderConfig,
	cmd *exec.Cmd,
) (baseprovider.TextToJSONProvider, error) {
	const defaultTimeout = 10 * time.Second
	httpClient := &http.Client{
		Timeout: defaultTimeout,
	}

	url, err := url.Parse(config.BaseURL())
	if err != nil {
		panic(err)
	}

	if config.model == "" {
		config.model = OLamaTextToJSONDefaultModelFast
	}

	// TODO(nullswan): Mutualize start code
	p := &TextToJSONProvider{
		config: config,
		client: api.NewClient(
			url,
			httpClient,
		),
		cmd: cmd,
	}

	if cmd != nil {
		err := waitForOllamaServer(p.client)
		if err != nil {
			return nil, fmt.Errorf("error waiting for ollama server: %w", err)
		}
	}

	for {
		listResp, err := p.client.List(context.Background())
		if err != nil {
			return nil, fmt.Errorf("error listing models: %w", err)
		}
		for _, model := range listResp.Models {
			if model.Name == config.model {
				return p, nil
			}
		}

		req := api.PullRequest{
			Model:  config.model,
			Stream: boolPtr(true),
		}

		progressCb := func(resp api.ProgressResponse) error {
			fmt.Printf(
				"Pulling %q: %s [%s/%s]\n",
				config.model,
				resp.Status,
				humanize.Bytes(uint64(resp.Completed)),
				humanize.Bytes(uint64(resp.Total)),
			)
			return nil
		}

		err = p.client.Pull(context.Background(), &req, progressCb)
		if err != nil {
			return nil, fmt.Errorf("error pulling model: %w", err)
		}
	}
}

func (p TextToJSONProvider) Close() error {
	if p.cmd != nil {
		// We started the server, so we should stop it
		err := stopOllamaServer(p.cmd)
		if err != nil {
			return fmt.Errorf("error stopping ollama server: %w", err)
		}
	}

	return nil
}

func (p TextToJSONProvider) GetModel() string {
	return p.config.model
}

func (p TextToJSONProvider) GenerateCompletion(
	ctx context.Context,
	messages []chat.Message,
	completionCh chan<- completion.Completion,
) error {
	req := completionRequestTextToJSON(p.config.model, messages)

	aggCompletion := ""
	resp := func(resp api.ChatResponse) error {
		if resp.Done {
			completionCh <- completion.NewCompletionTombStone(
				aggCompletion,
				p.config.model,
				completion.Usage{},
			)
			return nil
		}

		completionCh <- completion.NewCompletionData(
			resp.Message.Content,
		)
		aggCompletion += resp.Message.Content

		return nil
	}

	err := p.client.Chat(ctx, &req, resp)
	if err != nil {
		return fmt.Errorf("error creating completion stream: %w", err)
	}

	return nil
}

func completionRequestTextToJSON(
	model string,
	messages []chat.Message,
) api.ChatRequest {
	stream := true

	req := api.ChatRequest{
		Model:    model,
		Stream:   &stream,
		Messages: make([]api.Message, len(messages)),
		Format:   "json",
	}

	for i, m := range messages {
		req.Messages[i] = api.Message{
			Content: m.Content,
			Role:    m.Role.String(),
		}
	}

	return req
}
