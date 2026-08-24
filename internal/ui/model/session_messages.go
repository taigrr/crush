package model

import (
	"context"
	"image"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/taigrr/crush/internal/agent"
	"github.com/taigrr/crush/internal/message"
	"github.com/taigrr/crush/internal/pubsub"
	"github.com/taigrr/crush/internal/ui/chat"
)

// setSessionMessages sets the messages for the current session in the chat
func (m *UI) setSessionMessages(msgs []message.Message) tea.Cmd {
	var cmds []tea.Cmd
	// Build tool result map to link tool calls with their results
	msgPtrs := make([]*message.Message, len(msgs))
	for i := range msgs {
		msgPtrs[i] = &msgs[i]
	}
	toolResultMap := chat.BuildToolResultMap(msgPtrs)
	if len(msgPtrs) > 0 {
		m.lastUserMessageTime = msgPtrs[0].CreatedAt
	} else {
		// Reset so the sidebar's turn-elapsed indicator doesn't carry
		// over a stale timestamp from a previously viewed session.
		m.lastUserMessageTime = 0
	}

	// Add messages to chat with linked tool results
	items := make([]chat.MessageItem, 0, len(msgs)*2)
	imgCfg := m.imageConfig()
	for _, msg := range msgPtrs {
		switch msg.Role {
		case message.User:
			m.lastUserMessageTime = msg.CreatedAt
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap)...)
		case message.Assistant:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap)...)
			if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn {
				infoItem := chat.NewAssistantInfoItem(m.com.Styles, msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
				items = append(items, infoItem)
			}
		default:
			items = append(items, chat.ExtractMessageItems(m.com.Styles, msg, toolResultMap)...)
		}
	}

	for _, item := range items {
		if toolItem, ok := item.(chat.ToolMessageItem); ok {
			toolItem.SetImageConfig(imgCfg)
			if cmd := toolItem.TransmitImage(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if userItem, ok := item.(*chat.UserMessageItem); ok {
			userItem.SetImageConfig(imgCfg)
			if cmd := userItem.TransmitImages(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}

	// Load nested tool calls for agent/agentic_fetch tools.
	nestedCmds := m.loadNestedToolCalls(items)
	cmds = append(cmds, nestedCmds...)

	// If the user switches between sessions while the agent is working we want
	// to make sure the animations are shown.
	cmds = append(cmds, startItemAnimations(items...)...)

	m.chat.SetMessages(items...)
	if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	m.chat.SelectLast()
	return tea.Sequence(cmds...)
}

// loadNestedToolCalls recursively loads nested tool calls for agent/agentic_fetch tools.
func (m *UI) loadNestedToolCalls(items []chat.MessageItem) []tea.Cmd {
	var cmds []tea.Cmd
	imgCfg := m.imageConfig()

	for _, item := range items {
		nestedContainer, ok := item.(chat.NestedToolContainer)
		if !ok {
			continue
		}
		toolItem, ok := item.(chat.ToolMessageItem)
		if !ok {
			continue
		}

		tc := toolItem.ToolCall()
		messageID := toolItem.MessageID()

		// loadFromSession extracts the nested tool items recorded in a
		// child agent-tool session, if any. hadMessages reports whether
		// the session existed at all (had any messages), which is
		// distinct from whether it produced any tool items — a reviewer
		// may answer without calling a single tool.
		loadFromSession := func(sessionID string) (items []chat.ToolMessageItem, hadMessages bool) {
			nestedMsgs, err := m.com.Workspace.ListMessages(context.Background(), sessionID)
			if err != nil || len(nestedMsgs) == 0 {
				return nil, false
			}
			nestedMsgPtrs := make([]*message.Message, len(nestedMsgs))
			for i := range nestedMsgs {
				nestedMsgPtrs[i] = &nestedMsgs[i]
			}
			nestedToolResultMap := chat.BuildToolResultMap(nestedMsgPtrs)

			var nested []chat.ToolMessageItem
			for _, nestedMsg := range nestedMsgPtrs {
				nestedItems := chat.ExtractMessageItems(m.com.Styles, nestedMsg, nestedToolResultMap)
				for _, nestedItem := range nestedItems {
					nestedToolItem, ok := nestedItem.(chat.ToolMessageItem)
					if !ok {
						continue
					}
					// Mark nested tools as simple (compact) rendering.
					if simplifiable, ok := nestedToolItem.(chat.Compactable); ok {
						simplifiable.SetCompact(true)
					}
					nestedToolItem.SetImageConfig(imgCfg)
					if cmd := nestedToolItem.TransmitImage(); cmd != nil {
						cmds = append(cmds, cmd)
					}
					nested = append(nested, nestedToolItem)
				}
			}
			return nested, true
		}

		// applyNested recurses into any agent tools within and returns
		// the loaded set.
		applyNested := func(nested []chat.ToolMessageItem) []chat.ToolMessageItem {
			nestedMessageItems := make([]chat.MessageItem, len(nested))
			for i, nt := range nested {
				nestedMessageItems[i] = nt
			}
			cmds = append(cmds, m.loadNestedToolCalls(nestedMessageItems)...)
			return nested
		}

		// The review tool records one child session per reviewer
		// ("<toolCallID>-review-N"). Load each into its own group so the
		// reviewers render as separate sub-trees. Scan a fixed number of
		// reviewer indices and populate every group whose session had
		// messages. We must NOT break on the first empty slot: a reviewer
		// that failed early (session created, no messages) would
		// otherwise truncate the scan and drop later reviewers' groups.
		if grouped, ok := item.(chat.GroupedNestedToolContainer); ok {
			any := false
			// Bound the scan to the actual reviewer fan-out. Scan every
			// index (do not break on an empty slot, so a reviewer that
			// failed with no messages does not truncate later groups),
			// but no further — an unbounded probe would issue a
			// ListMessages query per index on every reload.
			for g := range agent.ReviewerCount {
				childID := agent.ReviewSubToolCallID(tc.ID, g)
				sessionID := m.com.Workspace.CreateAgentToolSessionID(messageID, childID)
				nested, hadMessages := loadFromSession(sessionID)
				if !hadMessages {
					continue
				}
				grouped.SetNestedToolsForGroup(g, applyNested(nested))
				any = true
			}
			if any {
				continue
			}
			// Fall through to the flat path only if no grouped sessions
			// exist at all (e.g. legacy single-session layout).
		}

		// Non-grouped containers (agent, agentic_fetch): a single child
		// session keyed by the tool call ID.
		agentSessionID := m.com.Workspace.CreateAgentToolSessionID(messageID, tc.ID)
		nestedTools, hadMessages := loadFromSession(agentSessionID)
		if !hadMessages {
			continue
		}
		nestedContainer.SetNestedTools(applyNested(nestedTools))
	}
	return cmds
}

// appendSessionMessage appends a new message to the current session in the chat
// if the message is a tool result it will update the corresponding tool call message
func (m *UI) appendSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd

	existing := m.chat.MessageItem(msg.ID)
	if existing != nil {
		// message already exists, skip
		return nil
	}

	switch msg.Role {
	case message.User:
		m.lastUserMessageTime = msg.CreatedAt
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil)
		imgCfg := m.imageConfig()
		for _, item := range items {
			if userItem, ok := item.(*chat.UserMessageItem); ok {
				userItem.SetImageConfig(imgCfg)
				if cmd := userItem.TransmitImages(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
			if animatable, ok := item.(chat.Animatable); ok {
				if cmd := animatable.StartAnimation(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
		m.chat.AppendMessages(items...)
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case message.Assistant:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil)
		cmds = append(cmds, startItemAnimations(items...)...)
		m.chat.AppendMessages(items...)
		if m.chat.Follow() {
			if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn {
			infoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(infoItem)
			if m.chat.Follow() {
				if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
					cmds = append(cmds, cmd)
				}
			}
		}
	case message.Shell:
		items := chat.ExtractMessageItems(m.com.Styles, &msg, nil)
		cmds = append(cmds, startItemAnimations(items...)...)
		m.chat.AppendMessages(items...)
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	case message.Tool:
		for _, tr := range msg.ToolResults() {
			toolItem := m.chat.MessageItem(tr.ToolCallID)
			if toolItem == nil {
				// we should have an item!
				continue
			}
			if toolMsgItem, ok := toolItem.(chat.ToolMessageItem); ok {
				toolMsgItem.SetResult(&tr)
				if cmd := toolMsgItem.TransmitImage(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				if m.chat.Follow() {
					if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
			}
		}
	}
	return tea.Sequence(cmds...)
}

// startItemAnimations starts the animation for every animatable item and
// returns the non-nil commands. It is the shared form of the loop used where
// items are added to the chat so animations show even when switching sessions
// mid-stream.
func startItemAnimations(items ...chat.MessageItem) []tea.Cmd {
	var cmds []tea.Cmd
	for _, item := range items {
		if animatable, ok := item.(chat.Animatable); ok {
			if cmd := animatable.StartAnimation(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return cmds
}

// exitLeftSidebarSearchOnBlur leaves the "/" filter when a focus transition
// moves focus off the (still-visible) left sidebar, so the sidebar never
// lingers in a filtered, no-focused-input state. wasFocused is whether the
// sidebar held focus before the transition. Sidebar-close paths already
// ExitSearch on their own.
func (m *UI) exitLeftSidebarSearchOnBlur(wasFocused bool) {
	if wasFocused && m.focus != uiFocusLeftSidebar {
		m.leftSidebar.ExitSearch()
	}
}

func (m *UI) handleClickFocus(msg tea.MouseClickMsg) (cmd tea.Cmd) {
	wasSidebar := m.focus == uiFocusLeftSidebar
	defer func() {
		m.exitLeftSidebarSearchOnBlur(wasSidebar)
		if wasSidebar && m.focus != uiFocusLeftSidebar {
			cmd = tea.Batch(cmd, m.cancelPreview())
		}
	}()
	switch {
	case m.state != uiChat:
		return nil
	case image.Pt(msg.X, msg.Y).In(m.layout.sidebar):
		if m.focus != uiFocusRightSidebar && !m.isCompact && !m.chatFullscreen && m.hasSession() && m.rightSidebarScrollable {
			m.focus = uiFocusRightSidebar
			m.textarea.Blur()
			m.chat.Blur()
		}
		return nil
	case m.focus != uiFocusEditor && image.Pt(msg.X, msg.Y).In(m.layout.editor):
		m.focus = uiFocusEditor
		cmd = m.textarea.Focus()
		m.chat.Blur()
	case m.focus != uiFocusMain && image.Pt(msg.X, msg.Y).In(m.layout.main):
		m.focus = uiFocusMain
		m.textarea.Blur()
		m.chat.Focus()
	}
	return cmd
}

// updateSessionMessage updates an existing message in the current session in
// the chat when an assistant message is updated it may include updated tool
// calls as well that is why we need to handle creating/updating each tool call
// message too.
func (m *UI) updateSessionMessage(msg message.Message) tea.Cmd {
	var cmds []tea.Cmd
	existingItem := m.chat.MessageItem(msg.ID)

	if existingItem != nil {
		if assistantItem, ok := existingItem.(*chat.AssistantMessageItem); ok {
			assistantItem.SetMessage(&msg)
		}
	}

	shouldRenderAssistant := chat.ShouldRenderAssistantMessage(&msg)
	isEndTurn := msg.FinishPart() != nil && msg.FinishPart().Reason == message.FinishReasonEndTurn
	// If the message of the assistant does not have any response just tool
	// calls we need to remove it, but keep the info item for end-of-turn
	// renders so the footer (model/provider/duration) remains visible when,
	// for example, a hook halts the turn.
	if !shouldRenderAssistant && len(msg.ToolCalls()) > 0 && existingItem != nil {
		m.chat.RemoveMessage(msg.ID)
		if !isEndTurn {
			if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); infoItem != nil {
				m.chat.RemoveMessage(chat.AssistantInfoID(msg.ID))
			}
		}
	}

	if isEndTurn {
		if infoItem := m.chat.MessageItem(chat.AssistantInfoID(msg.ID)); infoItem == nil {
			newInfoItem := chat.NewAssistantInfoItem(m.com.Styles, &msg, m.com.Config(), time.Unix(m.lastUserMessageTime, 0))
			m.chat.AppendMessages(newInfoItem)
		}
	}

	var items []chat.MessageItem
	for _, tc := range msg.ToolCalls() {
		existingToolItem := m.chat.MessageItem(tc.ID)
		if toolItem, ok := existingToolItem.(chat.ToolMessageItem); ok {
			existingToolCall := toolItem.ToolCall()
			// only update if finished state changed or input changed
			// to avoid clearing the cache
			if (tc.Finished && !existingToolCall.Finished) || tc.Input != existingToolCall.Input {
				toolItem.SetToolCall(tc)
			}
		}
		if existingToolItem == nil {
			item := chat.NewToolMessageItem(m.com.Styles, msg.ID, tc, nil, false)
			item.SetImageConfig(m.imageConfig())
			items = append(items, item)
		}
	}

	cmds = append(cmds, startItemAnimations(items...)...)

	m.chat.AppendMessages(items...)
	if m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.chat.SelectLast()
	}

	return tea.Sequence(cmds...)
}

// handleChildSessionMessage handles messages from child sessions (agent tools).
func (m *UI) handleChildSessionMessage(event pubsub.Event[message.Message]) tea.Cmd {
	var cmds []tea.Cmd

	// Only process messages with tool calls or results.
	if len(event.Payload.ToolCalls()) == 0 && len(event.Payload.ToolResults()) == 0 {
		return nil
	}

	// Check if this is an agent tool session and parse it.
	childSessionID := event.Payload.SessionID
	_, toolCallID, ok := m.com.Workspace.ParseAgentToolSessionID(childSessionID)
	if !ok {
		return nil
	}
	// The review tool fans out to multiple reviewer sub-agents whose
	// session tool-call IDs carry a "-review-N" suffix. Capture the
	// reviewer group index (if any), then strip the suffix so the events
	// map back to the single parent review tool call.
	reviewerGroup, isReviewer := agent.ReviewerIndexFromToolCallID(toolCallID)
	toolCallID = agent.StripReviewSuffix(toolCallID)

	// Find the parent agent tool item.
	var agentItem chat.NestedToolContainer
	for i := 0; i < m.chat.Len(); i++ {
		item := m.chat.MessageItem(toolCallID)
		if item == nil {
			continue
		}
		if agent, ok := item.(chat.NestedToolContainer); ok {
			if toolMessageItem, ok := item.(chat.ToolMessageItem); ok {
				if toolMessageItem.ToolCall().ID == toolCallID {
					// Verify this agent belongs to the correct parent message.
					// We can't directly check parentMessageID on the item, so we trust the session parsing.
					agentItem = agent
					break
				}
			}
		}
	}

	if agentItem == nil {
		return nil
	}

	// For the review tool, bucket the reviewer's nested tool calls into
	// its own group so each reviewer renders as a separate sub-tree.
	// Other containers (agent, agentic_fetch) use the flat list.
	grouped, useGroup := agentItem.(chat.GroupedNestedToolContainer)
	useGroup = useGroup && isReviewer

	var nestedTools []chat.ToolMessageItem
	if useGroup {
		nestedTools = grouped.NestedToolsForGroup(reviewerGroup)
	} else {
		nestedTools = agentItem.NestedTools()
	}

	// Update or create nested tool calls.
	for _, tc := range event.Payload.ToolCalls() {
		found := false
		for _, existingTool := range nestedTools {
			if existingTool.ToolCall().ID == tc.ID {
				existingTool.SetToolCall(tc)
				found = true
				break
			}
		}
		if !found {
			// Create a new nested tool item.
			nestedItem := chat.NewToolMessageItem(m.com.Styles, event.Payload.ID, tc, nil, false)
			nestedItem.SetImageConfig(m.imageConfig())
			if simplifiable, ok := nestedItem.(chat.Compactable); ok {
				simplifiable.SetCompact(true)
			}
			cmds = append(cmds, startItemAnimations(nestedItem)...)
			nestedTools = append(nestedTools, nestedItem)
		}
	}

	// Update nested tool results.
	for _, tr := range event.Payload.ToolResults() {
		for _, nestedTool := range nestedTools {
			if nestedTool.ToolCall().ID == tr.ToolCallID {
				nestedTool.SetResult(&tr)
				if cmd := nestedTool.TransmitImage(); cmd != nil {
					cmds = append(cmds, cmd)
				}
				break
			}
		}
	}

	// Update the agent item with the new nested tools.
	if useGroup {
		grouped.SetNestedToolsForGroup(reviewerGroup, nestedTools)
	} else {
		agentItem.SetNestedTools(nestedTools)
	}

	// Update the chat so it updates the index map for animations to work as expected
	m.chat.UpdateNestedToolIDs(toolCallID)

	if m.chat.Follow() {
		if cmd := m.chat.ScrollToBottomAndAnimate(); cmd != nil {
			cmds = append(cmds, cmd)
		}
		m.chat.SelectLast()
	}

	return tea.Sequence(cmds...)
}
