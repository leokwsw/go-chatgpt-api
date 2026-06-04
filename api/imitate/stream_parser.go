package imitate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type conversationStreamResult struct {
	Text                     string
	ConversationID           string
	MessageID                string
	FinishReason             string
	ContinueInfo             *ContinueInfo
	SawDone                  bool
	SawMessageStreamComplete bool
	SawLastMarker            bool
	SawStreamHandoff         bool
	Incomplete               bool
	TurnExchangeID           string
	GeneratedImageCandidates []generatedImagePointerCandidate
}

type conversationMessageState struct {
	ID                string
	Status            string
	EndTurn           bool
	EndTurnSeen       bool
	MetadataComplete  bool
	FinishDetailsSeen bool
	Text              StringStruct
	ActiveVisible     bool
}

type conversationStreamParser struct {
	onDelta       func(string) error
	imageText     func(Message) string
	result        conversationStreamResult
	message       conversationMessageState
	completedText strings.Builder
	sawV1         bool
}

func parseConversationStream(reader *bufio.Reader, onDelta func(string) error) (*conversationStreamResult, error) {
	return parseConversationStreamWithImageResolver(reader, onDelta, nil)
}

func parseConversationStreamWithImageResolver(reader *bufio.Reader, onDelta func(string) error, imageText func(Message) string) (*conversationStreamResult, error) {
	parser := &conversationStreamParser{
		onDelta:   onDelta,
		imageText: imageText,
		message: conversationMessageState{
			Text: StringStruct{Parts: make(map[int]string)},
		},
	}

	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if parseErr := parser.handleRawLine(line); parseErr != nil {
				return &parser.result, parseErr
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return &parser.result, err
		}
		if parser.result.SawDone {
			break
		}
	}

	parser.result.Text = parser.completedText.String() + parser.message.Text.Text
	if parser.result.MessageID == "" {
		parser.result.MessageID = parser.message.ID
	}
	parser.finalizeCompletionState()
	return &parser.result, nil
}

func (p *conversationStreamParser) handleRawLine(line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "event:") {
		return nil
	}
	if !strings.HasPrefix(line, "data:") {
		return nil
	}

	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "[DONE]" {
		p.result.SawDone = true
		return nil
	}
	return p.handleData(data)
}

func (p *conversationStreamParser) handleData(data string) error {
	var raw interface{}
	if err := json.Unmarshal([]byte(data), &raw); err != nil {
		return nil
	}

	if marker, ok := raw.(string); ok {
		if marker == "v1" {
			p.sawV1 = true
		}
		return nil
	}

	event, ok := raw.(map[string]interface{})
	if !ok {
		return nil
	}
	return p.handleEventMap(event, data)
}

func (p *conversationStreamParser) handleEventMap(event map[string]interface{}, originalJSON string) error {
	eventType := stringValue(event["type"])
	if eventType != "" {
		p.sawV1 = true
		p.updateConversationID(stringValue(event["conversation_id"]))
		switch eventType {
		case "resume_conversation_token", "input_message", "server_ste_metadata":
			return nil
		case "message_marker":
			markerEvent := stringValue(event["event"])
			marker := stringValue(event["marker"])
			messageID := stringValue(event["message_id"])
			if markerEvent == "first" && isVisibleMessageMarker(marker) {
				p.startVisibleMessage(messageID)
			}
			if markerEvent == "last" || marker == "last" || marker == "last_token" {
				p.result.SawLastMarker = true
				if messageID != "" {
					p.result.MessageID = messageID
				}
			}
			return nil
		case "message_stream_complete":
			p.result.SawMessageStreamComplete = true
			return nil
		case "stream_handoff":
			p.result.SawStreamHandoff = true
			p.result.TurnExchangeID = stringValue(event["turn_exchange_id"])
			return nil
		}
	}

	if err := p.handleEnvelopeSnapshot(event); err != nil {
		return err
	}

	if ops := patchOperationsFromEvent(event); len(ops) != 0 {
		return p.applyStreamPatchOperations(ops)
	}

	var response ChatGPTResponse
	if err := json.Unmarshal([]byte(originalJSON), &response); err != nil {
		return nil
	}
	if response.Error != nil {
		return fmt.Errorf("upstream conversation error: %v", response.Error)
	}
	if response.ConversationID != "" || response.Message.ID != "" {
		return p.applyConversationSnapshot(response)
	}
	return nil
}

func (p *conversationStreamParser) handleEnvelopeSnapshot(event map[string]interface{}) error {
	value, ok := event["v"].(map[string]interface{})
	if !ok {
		return nil
	}
	if value["message"] == nil && value["conversation_id"] == nil {
		return nil
	}

	payload, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var response ChatGPTResponse
	if err = json.Unmarshal(payload, &response); err != nil {
		return nil
	}
	if response.Error != nil {
		return fmt.Errorf("upstream conversation error: %v", response.Error)
	}
	return p.applyConversationSnapshot(response)
}

func (p *conversationStreamParser) applyConversationSnapshot(response ChatGPTResponse) error {
	p.updateConversationID(response.ConversationID)
	p.collectGeneratedImageCandidatesFromMessage(response.Message)
	if response.Message.ID == "" {
		return nil
	}

	if !isVisibleTextMessage(response.Message) {
		return nil
	}

	if p.message.ID != "" && response.Message.ID != p.message.ID {
		if p.message.ActiveVisible && p.message.Text.Text != "" {
			p.completedText.WriteString(p.message.Text.Text)
		}
		p.message = conversationMessageState{
			ID:            response.Message.ID,
			ActiveVisible: true,
			Text:          StringStruct{Parts: make(map[int]string)},
		}
	}

	p.message.ID = response.Message.ID
	p.result.MessageID = response.Message.ID
	p.message.ActiveVisible = true
	p.updateTerminalStateFromMessage(response.Message)

	currentText := p.extractSnapshotText(response.Message)
	delta := diffResponseText(p.message.Text.Text, currentText)
	p.message.Text.Text = currentText
	if response.Message.Content.ContentType == "multimodal_text" && p.imageText != nil {
		p.message.Text.Parts = map[int]string{0: currentText}
	} else {
		p.message.Text.Parts = extractTextParts(response.Message.Content)
		if len(p.message.Text.Parts) == 0 {
			p.message.Text.Parts = make(map[int]string)
		}
	}
	return p.emitDelta(delta)
}

func isVisibleMessageMarker(marker string) bool {
	return marker == "user_visible_token" || marker == "final_channel_token"
}

func (p *conversationStreamParser) startVisibleMessage(messageID string) {
	if messageID == "" {
		return
	}
	if p.message.ActiveVisible && p.message.ID == messageID {
		return
	}
	if p.message.ActiveVisible && p.message.Text.Text != "" {
		p.completedText.WriteString(p.message.Text.Text)
	}
	p.message = conversationMessageState{
		ID:            messageID,
		ActiveVisible: true,
		Text:          StringStruct{Parts: make(map[int]string)},
	}
	p.result.MessageID = messageID
}

func (p *conversationStreamParser) collectGeneratedImageCandidatesFromMessage(message Message) {
	if message.Author.Role != "assistant" && message.Author.Role != "tool" {
		return
	}
	payload, err := json.Marshal(message)
	if err != nil {
		return
	}
	var raw map[string]interface{}
	if err = json.Unmarshal(payload, &raw); err != nil {
		return
	}
	candidates := make([]generatedImagePointerCandidate, 0, 1)
	collectGeneratedImagePointerCandidates(raw, "", &candidates)
	candidates = bindGeneratedImageCandidateConversationID(candidates, p.result.ConversationID)
	p.result.GeneratedImageCandidates = mergeGeneratedImageCandidates(p.result.GeneratedImageCandidates, candidates)
}

func (p *conversationStreamParser) extractSnapshotText(message Message) string {
	if message.Content.ContentType == "multimodal_text" && p.imageText != nil {
		return p.imageText(message)
	}
	return extractTextContent(message.Content)
}

func (p *conversationStreamParser) updateTerminalStateFromMessage(message Message) {
	if message.Status != "" {
		p.message.Status = message.Status
	}
	if value, ok := boolFromInterface(message.EndTurn); ok {
		p.message.EndTurn = value
		p.message.EndTurnSeen = true
	}
	if message.Metadata.FinishDetails != nil && message.Metadata.FinishDetails.Type != "" {
		p.result.FinishReason = message.Metadata.FinishDetails.Type
		p.message.FinishDetailsSeen = true
	}
	if message.Metadata.IsComplete {
		p.message.MetadataComplete = true
	}
}

func (p *conversationStreamParser) applyStreamPatchOperations(operations []patchOperation) error {
	if p.message.Text.Parts == nil {
		p.message.Text.Parts = make(map[int]string)
	}

	previousFullText := p.message.Text.Text
	textUpdated := false
	for _, operation := range operations {
		p.collectGeneratedImageCandidatesFromValue(operation.Value, operation.Path)
		if operation.Path == "" && operation.Op == "" {
			if value, ok := operation.Value.(string); ok && value != "" {
				operation.Path = inferActiveContentPartPath(&p.message.Text)
				operation.Op = "append"
			}
		}

		switch operation.Path {
		case "/message/status":
			if value, ok := operation.Value.(string); ok {
				p.message.Status = value
			}
			continue
		case "/message/end_turn":
			if value, ok := boolFromInterface(operation.Value); ok {
				p.message.EndTurn = value
				p.message.EndTurnSeen = true
			}
			continue
		case "/message/metadata":
			p.updateMetadataTerminalState(operation.Value)
			continue
		case "/message/metadata/finish_details":
			p.updateFinishDetails(operation.Value)
			continue
		}

		partIndex, ok := extractContentPartIndex(operation.Path)
		if !ok {
			continue
		}
		value, ok := operation.Value.(string)
		if !ok && operation.Op != "remove" {
			continue
		}

		switch operation.Op {
		case "append":
			p.message.Text.Parts[partIndex] += value
			textUpdated = true
		case "replace":
			p.message.Text.Parts[partIndex] = value
			textUpdated = true
		case "remove":
			delete(p.message.Text.Parts, partIndex)
			textUpdated = true
		}
	}

	if !textUpdated {
		return nil
	}
	p.message.Text.Text = joinTextParts(p.message.Text.Parts)
	deltaText := diffResponseText(previousFullText, p.message.Text.Text)
	if !p.message.ActiveVisible {
		return nil
	}
	return p.emitDelta(deltaText)
}

func (p *conversationStreamParser) collectGeneratedImageCandidatesFromValue(value interface{}, path string) {
	candidates := make([]generatedImagePointerCandidate, 0, 1)
	collectGeneratedImagePointerCandidates(value, path, &candidates)
	candidates = bindGeneratedImageCandidateConversationID(candidates, p.result.ConversationID)
	p.result.GeneratedImageCandidates = mergeGeneratedImageCandidates(p.result.GeneratedImageCandidates, candidates)
}

func (p *conversationStreamParser) updateMetadataTerminalState(value interface{}) {
	metadata, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	if complete, ok := boolFromInterface(metadata["is_complete"]); ok && complete {
		p.message.MetadataComplete = true
	}
	if finishDetails, ok := metadata["finish_details"]; ok {
		p.updateFinishDetails(finishDetails)
	}
}

func (p *conversationStreamParser) updateFinishDetails(value interface{}) {
	finishDetails, ok := value.(map[string]interface{})
	if !ok {
		return
	}
	if finishType := stringValue(finishDetails["type"]); finishType != "" {
		p.result.FinishReason = finishType
		p.message.FinishDetailsSeen = true
	}
}

func (p *conversationStreamParser) emitDelta(delta string) error {
	if delta == "" || p.onDelta == nil {
		return nil
	}
	return p.onDelta(delta)
}

func (p *conversationStreamParser) updateConversationID(conversationID string) {
	if conversationID == "" {
		return
	}
	if p.result.ConversationID == "" {
		p.result.ConversationID = conversationID
	}
}

func (p *conversationStreamParser) finalizeCompletionState() {
	if p.result.FinishReason == "max_tokens" {
		if p.result.ConversationID == "" || p.result.MessageID == "" {
			p.result.Incomplete = true
			return
		}
		p.result.ContinueInfo = &ContinueInfo{
			ConversationID: p.result.ConversationID,
			ParentID:       p.result.MessageID,
		}
		return
	}

	if p.result.SawStreamHandoff || !p.result.SawDone {
		p.result.Incomplete = true
		return
	}
	if !p.sawV1 {
		if p.result.FinishReason == "" {
			p.result.FinishReason = "stop"
		}
		return
	}
	if !p.result.SawMessageStreamComplete {
		p.result.Incomplete = true
		return
	}
	if p.message.ID == "" {
		return
	}
	if p.message.Status != "finished_successfully" || !p.message.EndTurnSeen || !p.message.EndTurn || !p.message.MetadataComplete || !p.message.FinishDetailsSeen || p.result.FinishReason != "stop" {
		p.result.Incomplete = true
		return
	}
	if p.result.FinishReason == "" {
		p.result.FinishReason = "stop"
	}
}

func patchOperationsFromEvent(event map[string]interface{}) []patchOperation {
	if op := stringValue(event["o"]); op == "patch" {
		rawOperations, ok := event["v"].([]interface{})
		if !ok {
			return nil
		}
		operations := make([]patchOperation, 0, len(rawOperations))
		for _, rawOperation := range rawOperations {
			if operationMap, ok := rawOperation.(map[string]interface{}); ok {
				operations = append(operations, patchOperationFromMap(operationMap))
			}
		}
		return operations
	}
	if event["p"] != nil || event["o"] != nil {
		return []patchOperation{patchOperationFromMap(event)}
	}
	if value, ok := event["v"].(string); ok && value != "" {
		return []patchOperation{{Value: value}}
	}
	return nil
}

func patchOperationFromMap(value map[string]interface{}) patchOperation {
	return patchOperation{
		Path:  stringValue(value["p"]),
		Op:    stringValue(value["o"]),
		Value: value["v"],
	}
}

func boolFromInterface(value interface{}) (bool, bool) {
	flag, ok := value.(bool)
	return flag, ok
}
