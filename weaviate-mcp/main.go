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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/auth"
	weaviate_graphql "github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	weaviate_grpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/schema"
	"github.com/weaviate/weaviate/entities/models"
)

var _ mcp.Annotations

const (
	EnvWeaviateHostName     = "WEAVIATE_HOSTNAME"
	EnvWeaviateGRPCHostName = "WEAVIATE_GRPC_HOSTNAME"
	EnvWeaviateAPIKey       = "WEAVIATE_API_KEY"
	EnvHuggingFaceAPIKey    = "HUGGINGFACE_API_KEY"
)

type WeaviateMCP struct {
	client *weaviate.Client
	mcp    *mcp.Server
}

func main() {
	srvImpl := mcp.Implementation{
		Name: "weaviate",
	}
	srvOpts := mcp.ServerOptions{
		// Instructions: "", // TODO(zchee): fill
		KeepAlive: 30 * time.Second,
		HasTools:  true,
	}
	server := mcp.NewServer(&srvImpl, &srvOpts)

	client, err := NewWeaviate()
	if err != nil {
		log.Fatal(err)
	}
	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_schema",
		Description: "Get a weaviate schema",
	}, client.GetSchema)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "insert_one",
		Description: "Insert one object to collection",
	}, client.InsertOne)

	t := &mcp.LoggingTransport{Transport: new(mcp.StdioTransport), Writer: os.Stderr}
	if err := server.Run(context.Background(), t); err != nil {
		log.Printf("Server failed: %v", err)
	}
}

func NewWeaviate() (*WeaviateMCP, error) {
	cfg := weaviate.Config{
		Host:   os.Getenv(EnvWeaviateHostName),
		Scheme: "https",
		GrpcConfig: &weaviate_grpc.Config{
			Host:    os.Getenv(EnvWeaviateGRPCHostName),
			Secured: true,
		},
		AuthConfig: auth.ApiKey{
			Value: os.Getenv(EnvWeaviateAPIKey),
		},
		Headers: map[string]string{
			"X-HuggingFace-Api-Key": os.Getenv(EnvHuggingFaceAPIKey),
		},
	}
	client, err := weaviate.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("connect to weaviate: %w", err)
	}

	// Check the connection
	if _, err := client.Misc().ReadyChecker().Do(context.Background()); err != nil {
		return nil, fmt.Errorf("check the connection: %w", err)
	}

	return &WeaviateMCP{
		client: client,
	}, nil
}

func (w *WeaviateMCP) GetSchema(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, *schema.Dump, error) {
	scem, err := w.client.Schema().Getter().Do(context.Background())
	if err != nil {
		return nil, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: scem.Name,
			},
		},
	}, scem, nil
}

type InsertOneArgs struct {
	Collection string `json:"collection"`
	Properties any    `json:"properties"`
}

func (w *WeaviateMCP) InsertOne(ctx context.Context, _ *mcp.CallToolRequest, args InsertOneArgs) (*mcp.CallToolResult, *models.Object, error) {
	obj := models.Object{
		Class:      args.Collection,
		Properties: args.Properties,
	}

	// Use batch to leverage autoschema and gRPC
	resp, err := w.batchInsert(ctx, &obj)
	if err != nil {
		return nil, nil, fmt.Errorf("insert one object: %w", err)
	}

	return &mcp.CallToolResult{}, &resp[0].Object, nil
}

type QueryArgs struct {
	Collection       string   `json:"collection"`
	Query            string   `json:"query"`
	TargetProperties []string `json:"targetProperties"`
}

func (w *WeaviateMCP) Ouery(ctx context.Context, _ *mcp.CallToolRequest, args QueryArgs) (*mcp.CallToolResult, string, error) {
	hybrid := weaviate_graphql.HybridArgumentBuilder{}
	hybrid.WithQuery(args.Query)

	res, err := w.client.GraphQL().Get().
		WithClassName(args.Collection).WithHybrid(&hybrid).
		WithFields(func() []weaviate_graphql.Field {
			fields := make([]weaviate_graphql.Field, len(args.TargetProperties))
			for i, prop := range args.TargetProperties {
				fields[i] = weaviate_graphql.Field{Name: prop}
			}
			return fields
		}()...).
		Do(ctx)
	if err != nil {
		return nil, "", err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, "", fmt.Errorf("unmarshal query response: %w", err)
	}

	return &mcp.CallToolResult{}, string(b), nil
}

func (w *WeaviateMCP) batchInsert(ctx context.Context, objs ...*models.Object) ([]models.ObjectsGetResponse, error) {
	resp, err := w.client.Batch().ObjectsBatcher().WithObjects(objs...).Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("make insertion request: %w", err)
	}

	for _, res := range resp {
		if res.Result != nil && res.Result.Errors != nil && res.Result.Errors.Error != nil {
			for _, nestedErr := range res.Result.Errors.Error {
				err = errors.Join(err, errors.New(nestedErr.Message))
			}
		}
	}

	return resp, err
}
