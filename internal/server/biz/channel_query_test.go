package biz

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/privacy"
	"github.com/looplj/axonhub/internal/objects"
)

func TestChannelService_QueryChannels_WithModelFilter(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = privacy.DecisionContext(ctx, privacy.Allow)

	// Create test channels with different models
	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "Channel 1", []string{"gpt-4", "gpt-3.5-turbo"}, nil),
		createTestChannel(t, client, ctx, "Channel 2", []string{"gpt-4"}, nil),
		createTestChannel(t, client, ctx, "Channel 3", []string{"claude-3-opus"}, nil),
		createTestChannel(t, client, ctx, "Channel 4", []string{"gpt-4", "claude-3-opus"}, nil),
		createTestChannel(t, client, ctx, "Channel 5", []string{"gpt-3.5-turbo"}, nil),
		createTestChannel(t, client, ctx, "Channel 6", []string{"gpt-4"}, nil),
	}

	tests := []struct {
		name              string
		input             QueryChannelsInput
		expectedIDs       []int
		expectedHasNext   bool
		expectedHasPrev   bool
		expectedEdgeCount int
	}{
		{
			name: "filter by gpt-4 model - first 2",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-4"),
				First: lo.ToPtr(2),
			},
			expectedIDs:       []int{channels[0].ID, channels[1].ID},
			expectedHasNext:   true,
			expectedHasPrev:   false,
			expectedEdgeCount: 2,
		},
		{
			name: "filter by gpt-4 model - all",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-4"),
			},
			expectedIDs:       []int{channels[0].ID, channels[1].ID, channels[3].ID, channels[5].ID},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 4,
		},
		{
			name: "filter by claude-3-opus - first 2",
			input: QueryChannelsInput{
				Model: lo.ToPtr("claude-3-opus"),
				First: lo.ToPtr(2),
			},
			expectedIDs:       []int{channels[2].ID, channels[3].ID},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 2,
		},
		{
			name: "filter by gpt-3.5-turbo",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-3.5-turbo"),
			},
			expectedIDs:       []int{channels[0].ID, channels[4].ID},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 2,
		},
		{
			name: "filter by non-existent model",
			input: QueryChannelsInput{
				Model: lo.ToPtr("non-existent-model"),
			},
			expectedIDs:       []int{},
			expectedHasNext:   false,
			expectedHasPrev:   false,
			expectedEdgeCount: 0,
		},
		{
			name: "filter by gpt-4 with pagination - first 1",
			input: QueryChannelsInput{
				Model: lo.ToPtr("gpt-4"),
				First: lo.ToPtr(1),
			},
			expectedIDs:       []int{channels[0].ID},
			expectedHasNext:   true,
			expectedHasPrev:   false,
			expectedEdgeCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := svc.QueryChannels(ctx, tt.input)

			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Len(t, conn.Edges, tt.expectedEdgeCount)
			require.Equal(t, tt.expectedHasNext, conn.PageInfo.HasNextPage)
			require.Equal(t, tt.expectedHasPrev, conn.PageInfo.HasPreviousPage)

			// Verify returned channel IDs
			actualIDs := make([]int, len(conn.Edges))
			for i, edge := range conn.Edges {
				actualIDs[i] = edge.Node.ID
			}
			require.ElementsMatch(t, tt.expectedIDs, actualIDs)

			// Verify cursors are set when there are results
			if len(conn.Edges) > 0 {
				require.NotNil(t, conn.PageInfo.StartCursor)
				require.NotNil(t, conn.PageInfo.EndCursor)
			}
		})
	}
}

func TestChannelService_QueryChannels_WithModelFilter_Pagination(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = privacy.DecisionContext(ctx, privacy.Allow)

	// Create many channels to test pagination properly
	var channels []*ent.Channel
	for i := 1; i <= 10; i++ {
		var models []string
		if i%2 == 0 {
			// Even channels support gpt-4
			models = []string{"gpt-4"}
		} else {
			// Odd channels support claude
			models = []string{"claude-3-opus"}
		}
		ch := createTestChannel(t, client, ctx, "Channel "+string(rune('0'+i)), models, nil)
		channels = append(channels, ch)
	}

	// Test forward pagination through gpt-4 channels
	t.Run("forward pagination with model filter", func(t *testing.T) {
		// First page
		firstPage, err := svc.QueryChannels(ctx, QueryChannelsInput{
			Model: lo.ToPtr("gpt-4"),
			First: lo.ToPtr(2),
		})
		require.NoError(t, err)
		require.Len(t, firstPage.Edges, 2)
		require.True(t, firstPage.PageInfo.HasNextPage)
		require.False(t, firstPage.PageInfo.HasPreviousPage)

		// Second page
		secondPage, err := svc.QueryChannels(ctx, QueryChannelsInput{
			Model: lo.ToPtr("gpt-4"),
			First: lo.ToPtr(2),
			After: firstPage.PageInfo.EndCursor,
		})
		require.NoError(t, err)
		require.Len(t, secondPage.Edges, 2)
		require.True(t, secondPage.PageInfo.HasNextPage)
		require.True(t, secondPage.PageInfo.HasPreviousPage)

		// Last page
		lastPage, err := svc.QueryChannels(ctx, QueryChannelsInput{
			Model: lo.ToPtr("gpt-4"),
			First: lo.ToPtr(2),
			After: secondPage.PageInfo.EndCursor,
		})
		require.NoError(t, err)
		require.Len(t, lastPage.Edges, 1) // Only 5 gpt-4 channels total, so last page has 1
		require.False(t, lastPage.PageInfo.HasNextPage)
		require.True(t, lastPage.PageInfo.HasPreviousPage)

		// Verify no overlap between pages
		firstIDs := make([]int, len(firstPage.Edges))
		for i, edge := range firstPage.Edges {
			firstIDs[i] = edge.Node.ID
		}
		secondIDs := make([]int, len(secondPage.Edges))
		for i, edge := range secondPage.Edges {
			secondIDs[i] = edge.Node.ID
		}
		lastIDs := make([]int, len(lastPage.Edges))
		for i, edge := range lastPage.Edges {
			lastIDs[i] = edge.Node.ID
		}

		// No duplicates across pages
		allIDs := append(append(firstIDs, secondIDs...), lastIDs...)
		uniqueIDs := make(map[int]bool)
		for _, id := range allIDs {
			require.False(t, uniqueIDs[id], "Found duplicate ID: %d", id)
			uniqueIDs[id] = true
		}
		require.Len(t, uniqueIDs, 5) // Total 5 gpt-4 channels
	})
}

func TestChannelService_QueryChannels_WithModelMapping(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = privacy.DecisionContext(ctx, privacy.Allow)

	// Create channels with model mappings
	settings := &objects.ChannelSettings{
		ModelMappings: []objects.ModelMapping{
			{From: "gpt-4-latest", To: "gpt-4"},
			{From: "gpt-4-turbo", To: "gpt-4"},
		},
	}

	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "Channel 1", []string{"gpt-4"}, settings),
		createTestChannel(t, client, ctx, "Channel 2", []string{"gpt-3.5-turbo"}, nil),
		createTestChannel(t, client, ctx, "Channel 3", []string{"gpt-4"}, settings),
	}

	tests := []struct {
		name              string
		model             string
		expectedIDs       []int
		expectedEdgeCount int
	}{
		{
			name:              "query by actual model name",
			model:             "gpt-4",
			expectedIDs:       []int{channels[0].ID, channels[2].ID},
			expectedEdgeCount: 2,
		},
		{
			name:              "query by mapped model name",
			model:             "gpt-4-latest",
			expectedIDs:       []int{channels[0].ID, channels[2].ID},
			expectedEdgeCount: 2,
		},
		{
			name:              "query by another mapped model name",
			model:             "gpt-4-turbo",
			expectedIDs:       []int{channels[0].ID, channels[2].ID},
			expectedEdgeCount: 2,
		},
		{
			name:              "query by unmapped model",
			model:             "gpt-3.5-turbo",
			expectedIDs:       []int{channels[1].ID},
			expectedEdgeCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
				Model: &tt.model,
			})

			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Len(t, conn.Edges, tt.expectedEdgeCount)

			// Verify returned channel IDs
			actualIDs := make([]int, len(conn.Edges))
			for i, edge := range conn.Edges {
				actualIDs[i] = edge.Node.ID
			}
			require.ElementsMatch(t, tt.expectedIDs, actualIDs)
		})
	}
}

func TestChannelService_QueryChannels_WithExtraModelPrefix(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = privacy.DecisionContext(ctx, privacy.Allow)

	// Create channels with extra model prefix
	settings := &objects.ChannelSettings{
		ExtraModelPrefix: "deepseek",
	}

	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "DeepSeek Channel", []string{"deepseek-chat", "deepseek-reasoner"}, settings),
		createTestChannel(t, client, ctx, "OpenAI Channel", []string{"gpt-4"}, nil),
	}

	tests := []struct {
		name              string
		model             string
		expectedIDs       []int
		expectedEdgeCount int
	}{
		{
			name:              "query by model without prefix",
			model:             "deepseek-chat",
			expectedIDs:       []int{channels[0].ID},
			expectedEdgeCount: 1,
		},
		{
			name:              "query by model with prefix",
			model:             "deepseek/deepseek-chat",
			expectedIDs:       []int{channels[0].ID},
			expectedEdgeCount: 1,
		},
		{
			name:              "query by model with prefix - reasoner",
			model:             "deepseek/deepseek-reasoner",
			expectedIDs:       []int{channels[0].ID},
			expectedEdgeCount: 1,
		},
		{
			name:              "query by unsupported model with prefix",
			model:             "deepseek/gpt-4",
			expectedIDs:       []int{},
			expectedEdgeCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
				Model: &tt.model,
			})

			require.NoError(t, err)
			require.NotNil(t, conn)
			require.Len(t, conn.Edges, tt.expectedEdgeCount)

			// Verify returned channel IDs
			actualIDs := make([]int, len(conn.Edges))
			for i, edge := range conn.Edges {
				actualIDs[i] = edge.Node.ID
			}
			require.ElementsMatch(t, tt.expectedIDs, actualIDs)
		})
	}
}

func TestChannelService_QueryChannels_WithoutModelFilter(t *testing.T) {
	svc, client := setupTestChannelService(t)
	defer client.Close()

	ctx := context.Background()
	ctx = ent.NewContext(ctx, client)
	ctx = privacy.DecisionContext(ctx, privacy.Allow)

	// Create test channels
	channels := []*ent.Channel{
		createTestChannel(t, client, ctx, "Channel 1", []string{"gpt-4"}, nil),
		createTestChannel(t, client, ctx, "Channel 2", []string{"claude-3-opus"}, nil),
		createTestChannel(t, client, ctx, "Channel 3", []string{"gpt-3.5-turbo"}, nil),
	}

	t.Run("query without model filter", func(t *testing.T) {
		conn, err := svc.QueryChannels(ctx, QueryChannelsInput{})

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Len(t, conn.Edges, 3)

		// Verify all channels are returned
		actualIDs := make([]int, len(conn.Edges))
		for i, edge := range conn.Edges {
			actualIDs[i] = edge.Node.ID
		}
		expectedIDs := []int{channels[0].ID, channels[1].ID, channels[2].ID}
		require.ElementsMatch(t, expectedIDs, actualIDs)
	})

	t.Run("query without model filter with pagination", func(t *testing.T) {
		conn, err := svc.QueryChannels(ctx, QueryChannelsInput{
			First: lo.ToPtr(2),
		})

		require.NoError(t, err)
		require.NotNil(t, conn)
		require.Len(t, conn.Edges, 2)
		require.True(t, conn.PageInfo.HasNextPage)
		require.False(t, conn.PageInfo.HasPreviousPage)
	})
}

// Helper function to create test channel
func createTestChannel(
	t *testing.T,
	client *ent.Client,
	ctx context.Context,
	name string,
	models []string,
	settings *objects.ChannelSettings,
) *ent.Channel {
	t.Helper()

	builder := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName(name).
		SetBaseURL("https://api.openai.com/v1").
		SetCredentials(&objects.ChannelCredentials{APIKey: "test-key"}).
		SetSupportedModels(models).
		SetDefaultTestModel(models[0]).
		SetStatus(channel.StatusEnabled)

	if settings != nil {
		builder.SetSettings(settings)
	}

	ch, err := builder.Save(ctx)
	require.NoError(t, err)

	return ch
}
