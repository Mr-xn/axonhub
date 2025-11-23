package biz

import (
	"context"
	"sort"

	"entgo.io/contrib/entgql"
	"entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
	"github.com/looplj/axonhub/internal/ent"
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

	var connections ent.ChannelConnection

	var channels []*ent.Channel
	hasNextPage := true
	hasPreviousPage := true

	for i := 0; i < 10; i++ {
		page, err := query.Paginate(ctx, input.After, input.First, input.Before, input.Last,
			ent.WithChannelOrder(input.OrderBy),
		)
		if err != nil {
			return nil, err
		}

		enough := false

		for _, ch := range page.Edges {
			channelObj := Channel{Channel: ch.Node}
			if channelObj.IsModelSupported(*input.Model) {
				channels = append(channels, ch.Node)
			}
			if input.First != nil && len(channels) >= *input.First {
				enough = true
				break
			}
			if input.Last != nil && len(channels) >= *input.Last {
				enough = true
				break
			}
		}

		if enough {
			break
		}

		if input.Last != nil && !page.PageInfo.HasNextPage {
			hasNextPage = false
			break
		}

		if input.First != nil && !page.PageInfo.HasPreviousPage {
			hasPreviousPage = false
			break
		}
	}

	// Build page info
	pageInfo := ent.PageInfo{
		HasNextPage:     hasNextPage,
		HasPreviousPage: hasPreviousPage,
		StartCursor:     startCursor,
		EndCursor:       endCursor,
	}

	// Return connection
	return &ent.ChannelConnection{
		Edges:      edges,
		PageInfo:   pageInfo,
		TotalCount: totalCount,
	}, nil
}

func (svc *ChannelService) filteredChannelsByModel(ctx context.Context, connection *ent.ChannelConnection, model string) ([]*ent.Channel, error) {
	var channels []*ent.Channel
	for _, ch := range connection.Edges {
		channel := Channel{Channel: ch.Node}
		if channel.IsModelSupported(model) {
			channels = append(channels, ch.Node)
		}
	}
	return channels, nil
}

// sortChannels sorts channels based on the specified order.
func (svc *ChannelService) sortChannels(channels []*ent.Channel, orderBy *ent.ChannelOrder) []*ent.Channel {
	if orderBy == nil || orderBy.Field == nil {
		return channels
	}

	fieldName := orderBy.Field.String()
	direction := orderBy.Direction.String()

	sortedChannels := lo.Slice(channels, 0, len(channels))

	sort.Slice(sortedChannels, func(i, j int) bool {
		a := sortedChannels[i]
		b := sortedChannels[j]

		switch fieldName {
		case "CREATED_AT":
			if direction == "ASC" {
				return a.CreatedAt.Before(b.CreatedAt)
			}
			return a.CreatedAt.After(b.CreatedAt)
		case "UPDATED_AT":
			if direction == "ASC" {
				return a.UpdatedAt.Before(b.UpdatedAt)
			}
			return a.UpdatedAt.After(b.UpdatedAt)
		case "ORDERING_WEIGHT":
			if direction == "ASC" {
				return a.OrderingWeight < b.OrderingWeight
			}
			return a.OrderingWeight > b.OrderingWeight
		default:
			// Default to ID sorting if field is not recognized
			if direction == "ASC" {
				return a.ID < b.ID
			}
			return a.ID > b.ID
		}
	})

	return sortedChannels
}

// calculatePagination calculates the start and end indices for pagination.
func (svc *ChannelService) calculatePagination(
	channels []*ent.Channel,
	totalCount int,
	after *entgql.Cursor[int],
	first *int,
	before *entgql.Cursor[int],
	last *int,
) (int, int) {
	if totalCount == 0 {
		return 0, 0
	}

	startIndex := 0
	endIndex := totalCount

	// Handle after cursor
	if after != nil {
		for i, ch := range channels {
			if ch.ID == after.Value {
				startIndex = i + 1
				break
			}
		}
	}

	// Handle before cursor
	if before != nil {
		for i, ch := range channels {
			if ch.ID == before.Value {
				endIndex = i
				break
			}
		}
	}

	// Handle first limit
	if first != nil {
		limitEnd := startIndex + *first
		if limitEnd < endIndex {
			endIndex = limitEnd
		}
	}

	// Handle last limit
	if last != nil {
		if first == nil {
			// If only last is specified, take the last N items
			startIndex = endIndex - *last
			if startIndex < 0 {
				startIndex = 0
			}
		} else {
			// If both first and last are specified, last takes precedence for the window size
			windowSize := *last
			if windowSize < (endIndex - startIndex) {
				endIndex = startIndex + windowSize
			}
		}
	}

	return startIndex, endIndex
}
