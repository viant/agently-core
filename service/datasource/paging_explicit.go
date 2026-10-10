package datasource

import (
	"fmt"
	"github.com/viant/forge/backend/types"
	"math"
	"strings"
)

func validateExplicitPaging(ds *types.DataSource) error {
	if ds == nil || ds.Paging == nil || !ds.Paging.Enabled {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(ds.Paging.Mode)) {
	case "", "page", "offset":
		return nil
	default:
		return fmt.Errorf("unsupported datasource paging mode %q", ds.Paging.Mode)
	}
}

// Explicit modes describe already paged upstream results. They never slice the
// received rows or invent tool arguments; the caller supplies authored fields.
func applyExplicitPaging(rows []map[string]interface{}, info map[string]interface{}, ds *types.DataSource, args map[string]interface{}) ([]map[string]interface{}, map[string]interface{}) {
	out := map[string]interface{}{}
	for key, value := range info {
		out[key] = value
	}
	position, sizeName := "page", "size"
	if p := ds.Paging.Parameters; p != nil {
		if p.Page != "" {
			position = p.Page
		}
		if p.Size != "" {
			sizeName = p.Size
		}
	}
	size := intArg(args, sizeName, ds.Paging.Size)
	if size <= 0 {
		size = len(rows)
	}
	if size <= 0 {
		size = 1
	}
	page, offset := 1, 0
	mode := strings.ToLower(strings.TrimSpace(ds.Paging.Mode))
	if mode == "offset" {
		offset = intArg(args, position, 0)
		if offset < 0 {
			offset = 0
		}
		page = offset/size + 1
	} else {
		page = intArg(args, position, 1)
		if page < 1 {
			page = 1
		}
		offset = (page - 1) * size
	}
	out["page"], out["pageSize"], out["returnedCount"] = page, size, len(rows)
	total, known := explicitDataInfoTotalCount(info, ds)
	if ds.Paging.OpenEnded {
		delete(out, "pageCount")
		delete(out, "totalPages")
		delete(out, "totalCount")
		delete(out, "recordCount")
		known = false
	}
	if known {
		out["totalCount"] = total
		count := int(math.Ceil(float64(total) / float64(size)))
		if count < 1 {
			count = 1
		}
		out["pageCount"] = count
	}
	if _, present := out["hasMore"].(bool); !present {
		if known {
			out["hasMore"] = offset+len(rows) < total
		} else {
			out["hasMore"] = len(rows) >= size
		}
	}
	return rows, out
}
