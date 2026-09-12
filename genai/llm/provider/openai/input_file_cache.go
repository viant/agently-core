package openai

import (
	"context"
	"time"

	"github.com/viant/afs/storage"
	afsco "github.com/viant/afsc/openai"
	"github.com/viant/afsc/openai/assets"
)

type inputFileHandle struct {
	id      string
	expires time.Time
}

// Capture a manager and its credential identity atomically. A concurrent user's
// credential refresh must not change the manager used by this upload.
func (c *Client) inputFileManager(ctx context.Context) (storage.Manager, string, error) {
	key, err := c.apiKey(ctx)
	if err != nil {
		return nil, "", err
	}
	c.storageMgrMu.Lock()
	defer c.storageMgrMu.Unlock()
	if c.storageMgr == nil || c.storageMgrAPIKey != key || c.storageMgrBaseURL != c.BaseURL {
		c.storageMgrAPIKey = key
		c.storageMgrBaseURL = c.BaseURL
		c.storageMgr = afsco.New(assets.NewConfig(key, assets.WithBaseURL(c.BaseURL)))
	}
	return c.storageMgr, key, nil
}
