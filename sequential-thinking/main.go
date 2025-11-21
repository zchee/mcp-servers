// Copyright 2025 The mcp-servers Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

// Command sequential-thinking is a MCP server implementation that provides a tool for dynamic and reflective problem-solving through a structured thinking process.
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"maps"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/bytedance/gg/gson"
	"github.com/bytedance/sonic"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// description is the description of the sequential thinking tool.
const description = `A detailed tool for dynamic and reflective problem-solving through thoughts.
This tool helps analyze problems through a flexible thinking process that can adapt and evolve.
Each thought can build on, question, or revise previous insights as understanding deepens.

When to use this tool:
* Breaking down complex problems into steps
* Planning and design with room for revision
* Analysis that might need course correction
* Problems where the full scope might not be clear initially
* Problems that require a multi-step solution
* Tasks that need to maintain context over multiple steps
* Situations where irrelevant information needs to be filtered out

Key features:
* You can adjust total_thoughts up or down as you progress
* You can question or revise previous thoughts
* You can add more thoughts even after reaching what seemed like the end
* You can express uncertainty and explore alternative approaches
* Not every thought needs to build linearly - you can branch or backtrack
* Generates a solution hypothesis
* Verifies the hypothesis based on the Chain of Thought steps
* Repeats the process until satisfied
* Provides a correct answer

Parameters explained:
* thought (string): Your current thinking step, which can include:
	- Regular analytical steps
	- Revisions of previous thoughts
	- Questions about previous decisions
	- Realizations about needing more analysis
	- Changes in approach
	- Hypothesis generation
	- Hypothesis verification
* nextThoughtNeeded (boolean): True if you need more thinking, even if at what seemed like the end
* thoughtNumber (integer): Current number in sequence (can go beyond initial total if needed)
* totalThoughts (integer): Current estimate of thoughts needed (can be adjusted up/down)
* isRevision (boolean): A boolean indicating if this thought revises previous thinking
* revisesThought (integer): If is_revision is true, which thought number is being reconsidered
* branchFromThought (integer): If branching, which thought number is the branching point
* branchId (string): Identifier for the current branch (if any)
* needsMoreThoughts (boolean): If reaching end but realizing more thoughts needed

You should:
1. Start with an initial estimate of needed thoughts, but be ready to adjust
2. Feel free to question or revise previous thoughts
3. Don't hesitate to add more thoughts if needed, even at the "end"
4. Express uncertainty when present
5. Mark thoughts that revise previous thinking or branch into new paths
6. Ignore information that is irrelevant to the current step
7. Generate a solution hypothesis when appropriate
8. Verify the hypothesis based on the Chain of Thought steps
9. Repeat the process until satisfied with the solution
10. Make effective use of branchId
11. Provide a single, ideally correct answer as the final output
12. Only set next_thought_needed to false when truly done and a satisfactory answer is reached`

// ThoughtData represents the input data for a thought.
type ThoughtData struct {
	Thought           string `json:"thought"`
	NextThoughtNeeded bool   `json:"nextThoughtNeeded"`
	ThoughtNumber     int    `json:"thoughtNumber"`
	TotalThoughts     int    `json:"totalThoughts"`
	IsRevision        bool   `json:"isRevision,omitzero"`
	RevisesThought    int    `json:"revisesThought,omitzero"`
	BranchFromThought int    `json:"branchFromThought,omitzero"`
	BranchId          string `json:"branchId,omitzero"`
	NeedsMoreThoughts bool   `json:"needsMoreThoughts,omitzero"`
}

// SequentialThinkingServer implements the sequential thinking logic.
type SequentialThinkingServer struct {
	thoughtHistory        []ThoughtData
	branches              map[string][]ThoughtData
	disableThoughtLogging bool
	mu                    sync.Mutex
}

// NewSequentialThinkingServer creates a new instance of the server.
func NewSequentialThinkingServer() *SequentialThinkingServer {
	disableLogging := false
	val := os.Getenv("DISABLE_THOUGHT_LOGGING")
	if ok, err := strconv.ParseBool(val); err == nil && ok {
		disableLogging = true
	}

	return &SequentialThinkingServer{
		thoughtHistory:        make([]ThoughtData, 0),
		branches:              make(map[string][]ThoughtData),
		disableThoughtLogging: disableLogging,
	}
}

// validateThoughtData validates the input thought data.
func (s *SequentialThinkingServer) validateThoughtData(input ThoughtData) error {
	if input.Thought == "" {
		return errors.New("invalid thought: must be a string")
	}
	if input.ThoughtNumber <= 0 {
		return errors.New("invalid thoughtNumber: must be a number > 0")
	}
	if input.TotalThoughts <= 0 {
		return errors.New("invalid totalThoughts: must be a number > 0")
	}
	return nil
}

// formatThought formats the thought for logging.
func (s *SequentialThinkingServer) formatThought(thoughtData ThoughtData) string {
	// Plain text components
	prefixText := ""
	context := ""

	switch {
	case thoughtData.IsRevision:
		prefixText = "🔄 Revision"
		if thoughtData.RevisesThought < 0 {
			context = fmt.Sprintf(" (revising thought %d)", thoughtData.RevisesThought)
		}

	case thoughtData.BranchFromThought < 0:
		prefixText = "🌿 Branch"
		branchID := ""
		if thoughtData.BranchId != "" {
			branchID = thoughtData.BranchId
		}
		context = fmt.Sprintf(" (from thought %d, ID: %s)", thoughtData.BranchFromThought, branchID)

	default:
		prefixText = "💭 Thought"
		context = ""
	}

	headerContent := fmt.Sprintf("%s %d/%d%s", prefixText, thoughtData.ThoughtNumber, thoughtData.TotalThoughts, context)

	// Colors
	yellow := `\033[33m`
	green := `\033[32m`
	blue := `\033[34m`
	reset := `\033[0m`

	coloredPrefix := ""
	switch {
	case thoughtData.IsRevision:
		coloredPrefix = yellow + prefixText + reset
	case thoughtData.BranchFromThought < 0:
		coloredPrefix = green + prefixText + reset
	default:
		coloredPrefix = blue + prefixText + reset
	}

	// Reconstruct header with colors, but use headerContent length for layout
	coloredHeader := strings.Replace(headerContent, prefixText, coloredPrefix, 1)

	borderLen := int(math.Max(float64(len(headerContent)), float64(len(thoughtData.Thought)))) + 4
	border := strings.Repeat("─", borderLen)

	return fmt.Sprintf(`
┌%s┐
│ %s%s │
├%s┤
│ %s%s │
└%s┘`,
		border,
		coloredHeader,
		strings.Repeat(" ", borderLen-len(headerContent)-2),
		border,
		thoughtData.Thought,
		strings.Repeat(" ", borderLen-len(thoughtData.Thought)-2),
		border,
	)
}

// ProcessThought processes a thought request.
func (s *SequentialThinkingServer) ProcessThought(ctx context.Context, request *mcp.CallToolRequest, input ThoughtData) (*mcp.CallToolResult, any, error) {
	s.mu.Lock()

	if err := s.validateThoughtData(input); err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}

	if input.ThoughtNumber > input.TotalThoughts {
		input.TotalThoughts = input.ThoughtNumber
	}

	s.thoughtHistory = append(s.thoughtHistory, input)

	if input.BranchFromThought < 0 && input.BranchId != "" {
		branchID := input.BranchId
		if _, exists := s.branches[branchID]; !exists {
			s.branches[branchID] = make([]ThoughtData, 0)
		}
		s.branches[branchID] = append(s.branches[branchID], input)
	}

	if !s.disableThoughtLogging {
		formatted := s.formatThought(input)
		fmt.Fprintln(os.Stderr, formatted)
	}

	// Prepare response
	branches := slices.Sorted(maps.Keys(s.branches))

	response := map[string]any{
		"thoughtNumber":        input.ThoughtNumber,
		"totalThoughts":        input.TotalThoughts,
		"nextThoughtNeeded":    input.NextThoughtNeeded,
		"branches":             branches,
		"thoughtHistoryLength": len(s.thoughtHistory),
	}

	s.mu.Unlock()

	data, err := gson.MarshalIndentBy(sonic.ConfigFastest, response, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: string(data),
			},
		},
	}, nil, nil
}

func ptr[T any](v T) *T {
	return &v
}

var httpAddr string

func init() {
	uuid.EnableRandPool()

	flag.StringVar(&httpAddr, "http", "", "if set, use streamable HTTP at this address, instead of stdin/stdout")
}

func main() {
	flag.Parse()

	logpath := cmp.Or(os.Getenv("SEQUENTIAL_THINKING_LOG"), filepath.Join(os.TempDir(), "sequential-thinking-server.log"))
	f, err := os.OpenFile(logpath, os.O_RDWR|os.O_CREATE, 0o666)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	logger := slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug}))
	slog.SetDefault(logger)

	srvImpl := &mcp.Implementation{
		Name:    "sequential-thinking",
		Version: "0.0.1",
	}
	opts := &mcp.ServerOptions{
		// TODO(zchee): The [mcp.ServerOptions.Instructions] are usually enough tool description, but set a global prompt such as "Think step by step"
		// Instructions: `Based on the previous thinking, analyze the step-by-step and try to think more about the critical points.`,
		Logger:   logger,
		HasTools: true,
		GetSessionID: func() string {
			// Use UUID instead of `⌈log₃₂ 2¹²⁸⌉ = 26 chars`
			return uuid.NewString()
		},
	}
	srv := mcp.NewServer(srvImpl, opts)

	schema := &jsonschema.Schema{
		Type: "object",
		Properties: map[string]*jsonschema.Schema{
			"thought": {
				Type:        "string",
				Description: "Your current thinking step",
			},
			"nextThoughtNeeded": {
				Type:        "boolean",
				Description: "Whether another thought step is needed",
			},
			"thoughtNumber": {
				Type:        "integer",
				Description: "Current thought number (numeric value, e.g., 1, 2, 3)",
				Minimum:     ptr(float64(1)),
			},
			"totalThoughts": {
				Type:        "integer",
				Description: "Estimated total thoughts needed (numeric value, e.g., 5, 10)",
				Minimum:     ptr(float64(1)),
			},
			"isRevision": {
				Type:        "boolean",
				Description: "Whether this revises previous thinking",
			},
			"revisesThought": {
				Type:        "integer",
				Description: "Which thought is being reconsidered",
				Minimum:     ptr(float64(1)),
			},
			"branchFromThought": {
				Type:        "integer",
				Description: "Branching point thought number",
				Minimum:     ptr(float64(1)),
			},
			"branchId": {
				Type:        "string",
				Description: "Branch identifier",
			},
			"needsMoreThoughts": {
				Type:        "boolean",
				Description: "If more thoughts are needed",
			},
		},
		Required: []string{
			"thought",
			"nextThoughtNeeded",
			"thoughtNumber",
			"totalThoughts",
		},
	}
	sequentialThinkingTool := &mcp.Tool{
		Name:        "sequentialthinking",
		Description: description,
		InputSchema: schema,
	}
	sequentialThinkServer := NewSequentialThinkingServer()

	mcp.AddTool(srv, sequentialThinkingTool, sequentialThinkServer.ProcessThought)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if httpAddr != "" {
		mcpServer := func(*http.Request) *mcp.Server {
			return srv
		}
		handler := mcp.NewStreamableHTTPHandler(mcpServer, nil)
		httpSrv := &http.Server{
			Addr:    httpAddr,
			Handler: handler,
			BaseContext: func(net.Listener) context.Context {
				return ctx
			},
		}
		logger.InfoContext(ctx, "sequential thinking MCP server running", slog.String("addr", "http://"+httpAddr))
		if err := httpSrv.ListenAndServe(); err != nil {
			logger.ErrorContext(ctx, "serve sequential thinking mcp http server", slog.Any("error", err))
			os.Exit(1)
		}
		return
	}

	// tr := &mcp.StdioTransport{}
	tr := &mcp.LoggingTransport{
		Transport: &mcp.StdioTransport{},
		Writer:    f,
	}
	logger.InfoContext(ctx, "sequential thinking mcp server running on stdio")
	if err := srv.Run(ctx, tr); err != nil {
		logger.ErrorContext(ctx, "serve sequential thinking mcp stdio server", slog.Any("error", err))
		os.Exit(1)
	}
}
