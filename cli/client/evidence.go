package client

import (
	"context"
	"encoding/json"
	"net/url"
)

// GetRaw answers a GET of path with query as the bytes the Kates API wrote,
// for a caller that keeps them as evidence rather than decoding them into
// types that would drop the fields they don't name.
func (c *Client) GetRaw(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	return c.getBytes(ctx, withQuery(path, query))
}
