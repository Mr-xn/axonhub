package chat

import (
	"context"
	"fmt"

	"github.com/looplj/axonhub/internal/llm"
	"github.com/looplj/axonhub/internal/llm/pipeline"
	"github.com/looplj/axonhub/internal/log"
	"github.com/looplj/axonhub/internal/server/biz"
)

// persistRequestMiddleware handles Request entity creation and UsageLog persistence.
// It combines the createRequest logic with response persistence.
type persistRequestMiddleware struct {
	pipeline.DummyMiddleware

	inbound *PersistentInboundTransformer
}

func persistRequest(inbound *PersistentInboundTransformer) pipeline.Middleware {
	return &persistRequestMiddleware{
		inbound: inbound,
	}
}

func (m *persistRequestMiddleware) Name() string {
	return "persist-request"
}

// OnInboundLlmRequest creates the Request entity in the database.
// This was previously the createRequest middleware.
func (m *persistRequestMiddleware) OnInboundLlmRequest(ctx context.Context, llmRequest *llm.Request) (*llm.Request, error) {
	if m.inbound.state.Request != nil {
		return llmRequest, nil
	}

	request, err := m.inbound.state.RequestService.CreateRequest(
		ctx,
		llmRequest,
		m.inbound.state.RawRequest,
		m.inbound.APIFormat(),
	)
	if err != nil {
		return nil, err
	}

	m.inbound.state.Request = request

	return llmRequest, nil
}

// OnOutboundLlmResponse creates UsageLog from the response.
// This was previously in PersistentOutboundTransformer.TransformResponse.
func (m *persistRequestMiddleware) OnOutboundLlmResponse(ctx context.Context, llmResp *llm.Response) (*llm.Response, error) {
	state := m.inbound.state
	if state.Request == nil || llmResp == nil {
		return llmResp, nil
	}

	// Use context without cancellation to ensure persistence even if client canceled
	persistCtx := context.WithoutCancel(ctx)
	usage := llmResp.Usage

	_, err := state.UsageLogService.CreateUsageLogFromRequest(persistCtx, state.Request, state.RequestExec, usage)
	if err != nil {
		log.Warn(persistCtx, "Failed to create usage log from request", log.Cause(err))
	}

	return llmResp, nil
}

// applyApiKeyModelMapping creates a middleware that applies model mapping from API key profiles.
// This is the first step in the inbound pipeline.
func applyApiKeyModelMapping(inbound *PersistentInboundTransformer) pipeline.Middleware {
	return pipeline.OnLlmRequest("apply-model-mapping", func(ctx context.Context, llmRequest *llm.Request) (*llm.Request, error) {
		if llmRequest.Model == "" {
			return nil, fmt.Errorf("%w: request model is empty", biz.ErrInvalidModel)
		}

		// Apply model mapping from API key profiles if active profile exists
		if inbound.state.APIKey == nil {
			return llmRequest, nil
		}

		originalModel := llmRequest.Model
		mappedModel := inbound.state.ModelMapper.MapModel(ctx, inbound.state.APIKey, originalModel)

		if mappedModel != originalModel {
			llmRequest.Model = mappedModel
			log.Debug(ctx, "applied model mapping from API key profile",
				log.String("api_key_name", inbound.state.APIKey.Name),
				log.String("original_model", originalModel),
				log.String("mapped_model", mappedModel))
		}

		// Save the model for later use, e.g. retry from next channels, should use the original model to choose channel model.
		// This should be done after the api key level model mapping.
		// This should be done before the request is created.
		// The outbound transformer will restore the original model if it was mapped.
		if inbound.state.OriginalModel == "" {
			inbound.state.OriginalModel = llmRequest.Model
		} else {
			// Restore original model if it was mapped
			// This should not happen, the inbound should not be called twice.
			// Just in case, restore the original model.
			llmRequest.Model = inbound.state.OriginalModel
		}

		return llmRequest, nil
	})
}

// selectChannels creates a middleware that selects available channels for the model.
// This is the second step in the inbound pipeline, moved from outbound transformer.
// If no valid channels are found, it returns ErrInvalidModel to fail fast.
func selectChannels(inbound *PersistentInboundTransformer) pipeline.Middleware {
	return pipeline.OnLlmRequest("select-channels", func(ctx context.Context, llmRequest *llm.Request) (*llm.Request, error) {
		// Only select channels once
		if len(inbound.state.Channels) > 0 {
			return llmRequest, nil
		}

		channels, err := inbound.state.ChannelSelector.Select(ctx, llmRequest)
		if err != nil {
			return nil, err
		}

		log.Debug(ctx, "selected channels",
			log.Any("channels", channels),
			log.Any("model", llmRequest.Model),
		)

		if len(channels) == 0 {
			return nil, fmt.Errorf("%w: no valid channels found for model %s", biz.ErrInvalidModel, llmRequest.Model)
		}

		inbound.state.Channels = channels

		return llmRequest, nil
	})
}
