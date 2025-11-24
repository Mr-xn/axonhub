package biz

import (
	"context"

	"github.com/looplj/axonhub/internal/ent"

	"entgo.io/contrib/entgql"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/samber/lo"
)

// QueryChannelsInput represents the input for querying channels with additional filters.
type QueryChannelsInput struct {
	After   *entgql.Cursor[int]
	First   *int
	Before  *entgql.Cursor[int]
	Last    *int
	OrderBy *ent.ChannelOrder
	Where   *ent.ChannelWhereInput
	HasTag  *string
	Model   *string
}

// QueryChannels queries channels with the specified input parameters, including model filtering.
func (svc *ChannelService) QueryChannels(ctx context.Context, input QueryChannelsInput) (*ent.ChannelConnection, error) {
	// Build the base query
	var (
		query = svc.entFromContext(ctx).Channel.Query()
		err   error
	)

	// Apply standard filters
	if input.Where != nil {
		query, err = input.Where.Filter(query)
		if err != nil {
			return nil, err
		}
	}

	if input.HasTag != nil && *input.HasTag != "" {
		query = query.Where(func(s *sql.Selector) {
			s.Where(sqljson.ValueContains("tags", *input.HasTag))
		})
	}

	// If the model is not specified, return the query result directly.
	if input.Model == nil || *input.Model == "" {
		return query.Paginate(ctx, input.After, input.First, input.Before, input.Last,
			ent.WithChannelOrder(input.OrderBy),
		)
	}

	// When model filtering is required, we need to do in-memory pagination
	return svc.queryChannelsWithModelFilter(ctx, query, input)
}

// queryChannelsWithModelFilter performs model filtering with paginated fetching.
// It fetches channels in batches until we have enough matching results.
func (svc *ChannelService) queryChannelsWithModelFilter(
	ctx context.Context,
	query *ent.ChannelQuery,
	input QueryChannelsInput,
) (*ent.ChannelConnection, error) {
	// Get the order to use
	order := input.OrderBy
	if order == nil {
		order = ent.DefaultChannelOrder
	}

	// Determine pagination mode and how many results we need
	var needed int
	var isBackward bool

	if input.Last != nil {
		// Backward pagination
		isBackward = true
		needed = *input.Last + 1 // +1 to check hasPreviousPage
	} else if input.First != nil {
		// Forward pagination
		isBackward = false
		needed = *input.First + 1 // +1 to check hasNextPage
	} else {
		// No pagination specified, default to forward
		isBackward = false
		needed = 100 // Default limit
	}

	var filteredChannels []*ent.Channel
	var lastPageInfo *ent.PageInfo

	if isBackward {
		// Backward pagination: query from before cursor backwards
		filteredChannels, lastPageInfo = svc.fetchChannelsBackward(ctx, query, input, order, needed)
	} else {
		// Forward pagination: query from after cursor forwards
		filteredChannels, lastPageInfo = svc.fetchChannelsForward(ctx, query, input, order, needed)
	}

	// Build the final connection
	return svc.buildConnectionInMemory(filteredChannels, order, input.After, input.First, input.Before, input.Last, lastPageInfo), nil
}

// fetchChannelsForward fetches channels in forward direction (after -> forward).
func (svc *ChannelService) fetchChannelsForward(
	ctx context.Context,
	query *ent.ChannelQuery,
	input QueryChannelsInput,
	order *ent.ChannelOrder,
	needed int,
) ([]*ent.Channel, *ent.PageInfo) {
	var filteredChannels []*ent.Channel
	var currentAfter *ent.Cursor = input.After
	var lastPageInfo *ent.PageInfo
	const batchSize = 50     // Fetch 50 at a time
	const maxIterations = 20 // Safety limit to prevent infinite loops

	for range maxIterations {
		// Fetch a batch of channels (forward pagination)
		page, err := query.Paginate(ctx, currentAfter, lo.ToPtr(batchSize), input.Before, nil,
			ent.WithChannelOrder(order),
		)
		if err != nil {
			break
		}

		lastPageInfo = &page.PageInfo

		// Filter this batch by model support
		for _, edge := range page.Edges {
			channelObj := Channel{Channel: edge.Node}
			if channelObj.IsModelSupported(*input.Model) {
				filteredChannels = append(filteredChannels, edge.Node)

				// Check if we have enough results
				if len(filteredChannels) >= needed {
					break
				}
			}
		}

		// Stop if we have enough or no more pages
		if len(filteredChannels) >= needed || !page.PageInfo.HasNextPage {
			break
		}

		// Move to next page
		if len(page.Edges) > 0 {
			currentAfter = &page.Edges[len(page.Edges)-1].Cursor
		} else {
			break
		}
	}

	return filteredChannels, lastPageInfo
}

// fetchChannelsBackward fetches channels in backward direction (before -> backward).
func (svc *ChannelService) fetchChannelsBackward(
	ctx context.Context,
	query *ent.ChannelQuery,
	input QueryChannelsInput,
	order *ent.ChannelOrder,
	needed int,
) ([]*ent.Channel, *ent.PageInfo) {
	// For backward pagination, we collect all batches first, then reverse the order
	var allBatches [][]*ent.Channel
	var currentBefore *ent.Cursor = input.Before
	var lastPageInfo *ent.PageInfo
	const batchSize = 50     // Fetch 50 at a time
	const maxIterations = 20 // Safety limit to prevent infinite loops
	totalCollected := 0

	for range maxIterations {
		// Fetch a batch of channels (backward pagination)
		// Use 'last' instead of 'first' to go backwards
		page, err := query.Paginate(ctx, input.After, nil, currentBefore, lo.ToPtr(batchSize),
			ent.WithChannelOrder(order),
		)
		if err != nil {
			break
		}

		lastPageInfo = &page.PageInfo

		// Filter this batch by model support
		var batchFiltered []*ent.Channel
		for _, edge := range page.Edges {
			channelObj := Channel{Channel: edge.Node}
			if channelObj.IsModelSupported(*input.Model) {
				batchFiltered = append(batchFiltered, edge.Node)
			}
		}

		if len(batchFiltered) > 0 {
			allBatches = append(allBatches, batchFiltered)
			totalCollected += len(batchFiltered)
		}

		// Stop if we have enough or no more pages
		if totalCollected >= needed || !page.PageInfo.HasPreviousPage {
			break
		}

		// Move to previous page (the first cursor in this batch)
		if len(page.Edges) > 0 {
			currentBefore = &page.Edges[0].Cursor
		} else {
			break
		}
	}

	// Now reverse the batches and flatten
	var filteredChannels []*ent.Channel
	for i := len(allBatches) - 1; i >= 0; i-- {
		filteredChannels = append(filteredChannels, allBatches[i]...)
	}

	return filteredChannels, lastPageInfo
}

// buildConnectionInMemory builds a relay-style connection from filtered channels.
func (svc *ChannelService) buildConnectionInMemory(
	channels []*ent.Channel,
	order *ent.ChannelOrder,
	after *ent.Cursor,
	first *int,
	before *ent.Cursor,
	last *int,
	lastPageInfo *ent.PageInfo,
) *ent.ChannelConnection {
	conn := &ent.ChannelConnection{
		Edges:    []*ent.ChannelEdge{},
		PageInfo: ent.PageInfo{},
	}

	// Handle empty result
	if len(channels) == 0 {
		conn.TotalCount = 0
		return conn
	}

	// Determine pagination direction and slice
	var hasNextPage, hasPreviousPage bool
	var nodesToReturn []*ent.Channel

	if first != nil {
		// Forward pagination
		hasPreviousPage = after != nil
		if len(channels) > *first {
			// We have more than requested, so there's a next page
			hasNextPage = true
			nodesToReturn = channels[:*first]
		} else {
			// Check if database has more pages
			if lastPageInfo != nil {
				hasNextPage = lastPageInfo.HasNextPage
			} else {
				hasNextPage = false
			}
			nodesToReturn = channels
		}
	} else if last != nil {
		// Backward pagination
		if len(channels) > *last {
			// We have more than requested, so there's a previous page
			hasPreviousPage = true
			nodesToReturn = channels[len(channels)-*last:]
		} else {
			// Check if database has more pages (going backward)
			if lastPageInfo != nil {
				hasPreviousPage = lastPageInfo.HasPreviousPage
			} else {
				hasPreviousPage = false
			}
			nodesToReturn = channels
		}
		// For backward pagination, hasNextPage depends on before cursor
		hasNextPage = before != nil
	} else {
		// No pagination specified, return all
		hasPreviousPage = after != nil
		if lastPageInfo != nil {
			hasNextPage = lastPageInfo.HasNextPage
		} else {
			hasNextPage = false
		}
		nodesToReturn = channels
	}

	// Build edges using ToEdge method
	conn.Edges = make([]*ent.ChannelEdge, len(nodesToReturn))
	for i, ch := range nodesToReturn {
		conn.Edges[i] = ch.ToEdge(order)
	}

	// Set page info
	conn.PageInfo.HasNextPage = hasNextPage
	conn.PageInfo.HasPreviousPage = hasPreviousPage
	conn.TotalCount = len(nodesToReturn)

	if len(conn.Edges) > 0 {
		conn.PageInfo.StartCursor = &conn.Edges[0].Cursor
		conn.PageInfo.EndCursor = &conn.Edges[len(conn.Edges)-1].Cursor
	}

	return conn
}
