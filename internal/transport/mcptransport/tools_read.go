package mcptransport

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/edcnogueira/meowmcp/internal/service/whatsappservice"
)

type listChatsInput struct {
	Query  string `json:"query,omitempty" jsonschema:"filter by part of the chat name, phone number or JID"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max chats to return (default 20, max 200)"`
	Offset int    `json:"offset,omitempty" jsonschema:"chats to skip, for paging"`
}

type listChatsOutput struct {
	Chats []chatOutput `json:"chats"`
}

type getMessagesInput struct {
	Chat   string `json:"chat" jsonschema:"chat JID (from list_chats/search_contacts) or phone number with country code"`
	Limit  int    `json:"limit,omitempty" jsonschema:"max messages to return, newest first selected then returned oldest to newest (default 30, max 300)"`
	Before string `json:"before,omitempty" jsonschema:"only messages before this RFC 3339 timestamp or YYYY-MM-DD date (for paging back)"`
	After  string `json:"after,omitempty" jsonschema:"only messages at or after this RFC 3339 timestamp or YYYY-MM-DD date"`
}

type getMessagesOutput struct {
	Chat     chatOutput      `json:"chat"`
	Messages []messageOutput `json:"messages" jsonschema:"oldest to newest"`
}

type searchMessagesInput struct {
	Query string `json:"query" jsonschema:"text to search for (case-insensitive substring)"`
	Chat  string `json:"chat,omitempty" jsonschema:"restrict to one chat: JID or phone number"`
	Limit int    `json:"limit,omitempty" jsonschema:"max messages (default 30, max 300)"`
}

type messagesOutput struct {
	Messages []messageOutput `json:"messages" jsonschema:"oldest to newest"`
}

type searchContactsInput struct {
	Query string `json:"query" jsonschema:"part of a name, business name or phone number"`
	Limit int    `json:"limit,omitempty" jsonschema:"max contacts (default 20, max 100)"`
}

type searchContactsOutput struct {
	Contacts []contactOutput `json:"contacts"`
}

type checkNumbersInput struct {
	Phones []string `json:"phones" jsonschema:"phone numbers with country code, e.g. +5511999998888"`
}

type checkNumbersOutput struct {
	Results []numberCheckOutput `json:"results"`
}

func (t *Transport) registerReadTools(server *mcp.Server) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(false)}

	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_chats",
		Description: "List WhatsApp chats (direct and groups) ordered by most recent activity, each with its latest message. Use query to find a chat by name or number.",
		Annotations: readOnly,
	}, t.listChats)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_messages",
		Description: "Read the messages of one chat, returned oldest to newest. Media is described by media_type plus caption/file name; media content is not downloaded.",
		Annotations: readOnly,
	}, t.getMessages)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_messages",
		Description: "Search message text across all chats or within one chat.",
		Annotations: readOnly,
	}, t.searchMessages)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_contacts",
		Description: "Find contacts and chats by name, business name or phone number. Returns JIDs usable with get_messages and send_message.",
		Annotations: readOnly,
	}, t.searchContacts)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "check_numbers",
		Description: "Check whether phone numbers have WhatsApp accounts and get their chat JIDs (and verified business names). Use before contacting a new number, e.g. a clinic.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: new(true)},
	}, t.checkNumbers)
}

func (t *Transport) listChats(ctx context.Context, _ *mcp.CallToolRequest, in listChatsInput) (*mcp.CallToolResult, listChatsOutput, error) {
	chats, err := t.service.ListChats(ctx, &whatsappservice.ListChatsRequest{Query: in.Query, Limit: in.Limit, Offset: in.Offset})
	if err != nil {
		return nil, listChatsOutput{}, err
	}
	out := listChatsOutput{Chats: make([]chatOutput, len(chats))}
	for i, c := range chats {
		out.Chats[i] = toChatOutput(c)
	}
	return nil, out, nil
}

func (t *Transport) getMessages(ctx context.Context, _ *mcp.CallToolRequest, in getMessagesInput) (*mcp.CallToolResult, getMessagesOutput, error) {
	before, err := parseTime("before", in.Before)
	if err != nil {
		return nil, getMessagesOutput{}, err
	}
	after, err := parseTime("after", in.After)
	if err != nil {
		return nil, getMessagesOutput{}, err
	}
	resp, err := t.service.GetMessages(ctx, &whatsappservice.GetMessagesRequest{
		Chat:   in.Chat,
		Limit:  in.Limit,
		Before: before,
		After:  after,
	})
	if err != nil {
		return nil, getMessagesOutput{}, err
	}
	return nil, getMessagesOutput{Chat: toChatOutput(resp.Chat), Messages: toMessageOutputs(resp.Messages)}, nil
}

func (t *Transport) searchMessages(ctx context.Context, _ *mcp.CallToolRequest, in searchMessagesInput) (*mcp.CallToolResult, messagesOutput, error) {
	msgs, err := t.service.SearchMessages(ctx, &whatsappservice.SearchMessagesRequest{Query: in.Query, Chat: in.Chat, Limit: in.Limit})
	if err != nil {
		return nil, messagesOutput{}, err
	}
	return nil, messagesOutput{Messages: toMessageOutputs(msgs)}, nil
}

func (t *Transport) searchContacts(ctx context.Context, _ *mcp.CallToolRequest, in searchContactsInput) (*mcp.CallToolResult, searchContactsOutput, error) {
	contacts, err := t.service.SearchContacts(ctx, &whatsappservice.SearchContactsRequest{Query: in.Query, Limit: in.Limit})
	if err != nil {
		return nil, searchContactsOutput{}, err
	}
	out := searchContactsOutput{Contacts: make([]contactOutput, len(contacts))}
	for i, c := range contacts {
		out.Contacts[i] = contactOutput{JID: c.JID, Phone: c.Phone, Name: c.Name, PushName: c.PushName, BusinessName: c.BusinessName}
	}
	return nil, out, nil
}

func (t *Transport) checkNumbers(ctx context.Context, _ *mcp.CallToolRequest, in checkNumbersInput) (*mcp.CallToolResult, checkNumbersOutput, error) {
	checks, err := t.service.CheckNumbers(ctx, in.Phones)
	if err != nil {
		return nil, checkNumbersOutput{}, err
	}
	out := checkNumbersOutput{Results: make([]numberCheckOutput, len(checks))}
	for i, c := range checks {
		out.Results[i] = numberCheckOutput{Query: c.Query, OnWhatsApp: c.OnWhatsApp, JID: c.JID, BusinessName: c.BusinessName}
	}
	return nil, out, nil
}
