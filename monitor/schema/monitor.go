package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/slack-go/slack"
)

const (
	openAISchemaURL = "https://raw.githubusercontent.com/openai/openai-openapi/master/openapi.yaml"
	cacheDir        = "cache/schemas"
)

type Monitor struct {
	slackToken     string
	slackChannelID string
	cacheBasePath  string
}

func NewMonitor(slackToken, slackChannelID string) *Monitor {
	return &Monitor{
		slackToken:     slackToken,
		slackChannelID: slackChannelID,
		cacheBasePath:  cacheDir,
	}
}

// CheckOpenAISchema fetches the latest OpenAPI schema and compares it with the cached version
func (m *Monitor) CheckOpenAISchema(ctx context.Context) error {
	// Create cache directory if it doesn't exist
	if err := os.MkdirAll(m.cacheBasePath, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Fetch latest schema
	latestSchema, err := m.fetchOpenAPISchema(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch latest schema: %w", err)
	}

	// Load cached schema
	cachedSchema, err := m.loadCachedSchema()
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to load cached schema: %w", err)
	}

	// If no cached schema exists, save the current one and return
	if os.IsNotExist(err) {
		if err := m.cacheSchema(latestSchema); err != nil {
			return fmt.Errorf("failed to cache schema: %w", err)
		}
		return nil
	}

	// Compare schemas
	changes := m.compareSchemas(cachedSchema, latestSchema)
	if len(changes) > 0 {
		// Update cache with new schema
		if err := m.cacheSchema(latestSchema); err != nil {
			return fmt.Errorf("failed to update cached schema: %w", err)
		}

		// Notify about changes
		if err := m.notifyChanges(changes); err != nil {
			return fmt.Errorf("failed to notify changes: %w", err)
		}
	}

	return nil
}

func (m *Monitor) fetchOpenAPISchema(ctx context.Context) (*openapi3.T, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, openAISchemaURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	loader := openapi3.NewLoader()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return loader.LoadFromData(body)
}

func (m *Monitor) loadCachedSchema() (*openapi3.T, error) {
	data, err := os.ReadFile(filepath.Join(m.cacheBasePath, "openai-schema.json"))
	if err != nil {
		return nil, err
	}

	loader := openapi3.NewLoader()
	return loader.LoadFromData(data)
}

func (m *Monitor) cacheSchema(schema *openapi3.T) error {
	data, err := json.Marshal(schema)
	if err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(m.cacheBasePath, "openai-schema.json"), data, 0644)
}

type SchemaChange struct {
	Path     string
	OldValue interface{}
	NewValue interface{}
}

func (m *Monitor) compareSchemas(old, new *openapi3.T) []SchemaChange {
	var changes []SchemaChange

	// Compare paths and operations
	for path, pathItem := range new.Paths.Map() {
		oldPathItem := old.Paths.Find(path)
		if oldPathItem == nil {
			changes = append(changes, SchemaChange{
				Path:     fmt.Sprintf("Path %s", path),
				OldValue: nil,
				NewValue: "New endpoint added",
			})
			continue
		}

		// Compare operations
		operations := []string{"Get", "Post", "Put", "Delete", "Options", "Head", "Patch", "Trace"}
		for _, op := range operations {
			newOp := pathItem.GetOperation(op)
			oldOp := oldPathItem.GetOperation(op)

			if newOp == nil && oldOp == nil {
				continue
			}

			if (newOp == nil && oldOp != nil) || (newOp != nil && oldOp == nil) {
				changes = append(changes, SchemaChange{
					Path:     fmt.Sprintf("%s %s", op, path),
					OldValue: oldOp != nil,
					NewValue: newOp != nil,
				})
				continue
			}

			// Compare request bodies
			if !m.compareRequestBodies(oldOp.RequestBody, newOp.RequestBody) {
				changes = append(changes, SchemaChange{
					Path:     fmt.Sprintf("%s %s RequestBody", op, path),
					OldValue: "Changed",
					NewValue: "Changed",
				})
			}

			// Compare responses
			if newOp.Responses != nil && newOp.Responses.Map() != nil {
				for code, newResp := range newOp.Responses.Map() {
					var oldResp *openapi3.ResponseRef
					if oldOp.Responses != nil && oldOp.Responses.Map() != nil {
						oldResp = oldOp.Responses.Map()[code]
					}
					if oldResp == nil || !m.compareResponses(oldResp, newResp) {
						changes = append(changes, SchemaChange{
							Path:     fmt.Sprintf("%s %s Response %s", op, path, code),
							OldValue: oldResp != nil,
							NewValue: "Changed",
						})
					}
				}
			}
		}
	}

	return changes
}

func (m *Monitor) compareRequestBodies(old, new *openapi3.RequestBodyRef) bool {
	if (old == nil) != (new == nil) {
		return false
	}
	if old == nil && new == nil {
		return true
	}
	// Simple comparison - just check if they're different
	// In a production environment, you might want to do a more detailed comparison
	return old.Value.Description == new.Value.Description
}

func (m *Monitor) compareResponses(old, new *openapi3.ResponseRef) bool {
	if (old == nil) != (new == nil) {
		return false
	}
	if old == nil && new == nil {
		return true
	}
	// Simple comparison - just check if they're different
	return old.Value.Description == new.Value.Description
}

func (m *Monitor) notifyChanges(changes []SchemaChange) error {
	if m.slackToken == "" || m.slackChannelID == "" {
		return nil
	}

	api := slack.New(m.slackToken)

	var blocks []slack.Block
	blocks = append(blocks, slack.NewHeaderBlock(slack.NewTextBlockObject("plain_text", "OpenAI API Schema Changes Detected", false, false)))

	for _, change := range changes {
		text := fmt.Sprintf("*Path:* %s\nOld: %v\nNew: %v", change.Path, change.OldValue, change.NewValue)
		blocks = append(blocks, slack.NewSectionBlock(slack.NewTextBlockObject("mrkdwn", text, false, false), nil, nil))
	}

	_, _, err := api.PostMessage(
		m.slackChannelID,
		slack.MsgOptionBlocks(blocks...),
	)
	return err
}
