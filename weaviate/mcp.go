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

package main

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const instructions = `
Extract the Following Information:

- Code Snippets: Save the actual code for future reference.
- Explanation: Document a clear description of what the code does and how it works.
- Related Technical Details: Include information about the programming language, dependencies, and system specifications.
- Key Features: Highlight the main functionalities and important aspects of the snippet.`

type mcpServer struct {
	*mcp.Server
}

func NewMCP() *mcpServer {
	srvImpl := mcp.Implementation{
		Name:    "weaviate-mcp",
		Version: version,
	}
	srvOpts := mcp.ServerOptions{
		Instructions: instructions,
		KeepAlive:    30 * time.Second,
		HasPrompts:   true,
		HasResources: true,
		HasTools:     true,
	}

	return &mcpServer{
		Server: mcp.NewServer(&srvImpl, &srvOpts),
	}
}

func (s *mcpServer) AddTools(client *weaviateClient) {
	getSchemaTool := &mcp.Tool{
		Name:        "get_schema",
		Description: "Get a weaviate schema",
	}
	mcp.AddTool(s.Server, getSchemaTool, client.GetSchema)

	createSchemaClassTool := &mcp.Tool{
		Name:        "create_schema_class",
		Description: "Create a schema class",
	}
	mcp.AddTool(s.Server, createSchemaClassTool, client.CreateSchemaClass)

	insertOneTool := &mcp.Tool{
		Name:        "insert_one",
		Description: "Insert one object to collection",
	}
	mcp.AddTool(s.Server, insertOneTool, client.InsertOne)

	queryTool := &mcp.Tool{
		Name:        "query",
		Description: "Query data within Weaviate using hybrid search",
	}
	mcp.AddTool(s.Server, queryTool, client.Query)
}

func (s *mcpServer) AddPrompts(client *weaviateClient) {
	prompt := &mcp.Prompt{
		Name:        "get_schema",
		Description: "Get a weaviate schema",
		Arguments:   []*mcp.PromptArgument{},
	}
	promptHandler := func(ctx context.Context, gpr *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return nil, nil
	}
	s.AddPrompt(prompt, promptHandler)
}
