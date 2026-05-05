package scratchpad

type MemorizeInput struct {
	Key         string `json:"key" description:"Exact scratchpad key to create or replace."`
	Description string `json:"description" description:"Short description shown by list."`
	Body        string `json:"body" description:"Scratchpad note body."`
}

type MemorizeOutput struct {
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
}

type AppendInput struct {
	Key         string `json:"key" description:"Exact scratchpad key to append to."`
	Body        string `json:"body" description:"Scratchpad note body to append."`
	Description string `json:"description,omitempty" description:"Optional note-level description. Preserved when omitted on existing notes; defaults to key when creating."`
}

type AppendOutput struct {
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Created     bool   `json:"created,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ListInput struct{}

type Entry struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
}

type ListOutput struct {
	Entries []Entry `json:"entries"`
	Status  string  `json:"status,omitempty"`
	Error   string  `json:"error,omitempty"`
}

type FetchInput struct {
	Key string `json:"key" description:"Exact scratchpad key to fetch."`
}

type FetchOutput struct {
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
	Body        string `json:"body,omitempty"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Status      string `json:"status,omitempty"`
	Error       string `json:"error,omitempty"`
}

type noteFile struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	Body        string `json:"body"`
	UserID      string `json:"userId"`
	UpdatedAt   string `json:"updatedAt"`
}
