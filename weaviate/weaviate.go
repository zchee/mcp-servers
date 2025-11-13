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
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptrace"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weaviate/weaviate-go-client/v5/weaviate"
	weaviate_graphql "github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	weaviate_grpc "github.com/weaviate/weaviate-go-client/v5/weaviate/grpc"
	"github.com/weaviate/weaviate/entities/models"
	"github.com/weaviate/weaviate/entities/schema"
	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
)

type weaviateClient struct {
	*weaviate.Client
}

// NewWeaviate creates a new weaviate client.
func NewWeaviate(ctx context.Context) (*weaviateClient, error) {
	cc := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport.(*http.Transport).Clone(),
			otelhttp.WithClientTrace(func(ctx context.Context) *httptrace.ClientTrace {
				return otelhttptrace.NewClientTrace(ctx)
			}),
		),
	}
	_ = oauth2.NewClient
	cfg := weaviate.Config{
		Host:             os.Getenv(envWeaviateURL),
		Scheme:           "https",
		ConnectionClient: cc,
		GrpcConfig: &weaviate_grpc.Config{
			Host:    os.Getenv(envWeaviateGRPCURL),
			Secured: true,
		},
		// AuthConfig: auth.ApiKey{
		// 	Value: os.Getenv(envWeaviateAPIKey),
		// },
		Headers: map[string]string{
			"Authorization":         "Bearer " + os.Getenv(envWeaviateAPIKey),
			"X-HuggingFace-Api-Key": os.Getenv(envHuggingFaceAPIKey),
			"X-VoyageAI-Api-Key":    os.Getenv(envVoyageAIAPIKey),
			"X-Cohere-Api-Key":      os.Getenv(envCohereAPIKey),
			"X-JinaAI-Api-Key":      os.Getenv(envJinaAIAPIKey),
		},
	}

	client, err := weaviate.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("create to weaviate client: %w", err)
	}

	// Check the connection
	if _, err := client.Misc().ReadyChecker().Do(ctx); err != nil {
		return nil, fmt.Errorf("check the weaviate connection: %w", err)
	}

	return &weaviateClient{
		Client: client,
	}, nil
}

// GetSchema get a weaviate schema.
func (w *weaviateClient) GetSchema(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	scm, err := w.Schema().Getter().Do(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get schema: %w", err)
	}
	data, err := json.Marshal(scm.Schema)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal schema: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: string(data),
			},
		},
	}, nil, nil
}

// CreateSchemaClass creates a schema class.
func (w *weaviateClient) CreateSchemaClass(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
	className := "Go"
	hfClass := &models.Class{
		Class: className,
		Properties: []*models.Property{
			{
				Name:     "title",
				DataType: schema.DataTypeText.PropString(),
			},
			{
				Name:     "description",
				DataType: schema.DataTypeText.PropString(),
			},
			{
				Name:     "go_version",
				DataType: schema.DataTypeText.PropString(),
			},
			{
				Name:     "project",
				DataType: schema.DataTypeText.PropString(),
			},
			{
				Name:     "module_path",
				DataType: schema.DataTypeText.PropString(),
			},
			{
				Name:     "best_practices",
				DataType: schema.DataTypeTextArray.PropString(),
			},
			{
				Name:     "performance_optimizations",
				DataType: schema.DataTypeTextArray.PropString(),
			},
			{
				Name:     "code_snippet",
				DataType: schema.DataTypeTextArray.PropString(),
			},
		},
		VectorConfig: map[string]models.VectorConfig{
			"title": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"title"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
			"description": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"description"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
			"go_version": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"go_version"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
			"project_module_path": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"project", "module_path"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
			"go_version_best_practices": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"go_version", "best_practices"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
			"go_version_performance_optimizations": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"go_version", "performance_optimizations"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
			"go_version_code_snippet": {
				VectorIndexType: "hnsw",
				Vectorizer: map[string]any{
					"text2vec-huggingface": map[string]any{
						"model":            "BallAdMyFi/qwen3-jailbreaking-embedding-v3",
						"sourceProperties": []string{"go_version", "code_snippet"},
						"waitForModel":     true,
						"useCache":         true,
						"useGPU":           true,
					},
				},
			},
		},
	}

	if err := w.Schema().ClassCreator().WithClass(hfClass).Do(ctx); err != nil {
		return nil, nil, fmt.Errorf("create schema class: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: fmt.Sprintf("created %q schema class", className),
			},
		},
	}, nil, nil
}

type insertOneArgs struct {
	Collection string `json:"collection" jsonschema:"collection name"`
	Properties any    `json:"properties" jsonschema:"insert properties"`
}

func (w *weaviateClient) InsertOne(ctx context.Context, _ *mcp.CallToolRequest, args insertOneArgs) (*mcp.CallToolResult, any, error) {
	obj := models.Object{
		Class:      args.Collection,
		Properties: args.Properties,
	}

	// Use batch to leverage autoschema and gRPC
	_, err := w.batchInsert(ctx, &obj)
	if err != nil {
		return nil, nil, fmt.Errorf("insert one object: %w", err)
	}

	return &mcp.CallToolResult{}, nil, nil
}

type queryArgs struct {
	Collection       string   `json:"collection" jsonschema:"collection name"`
	Query            string   `json:"query" jsonschema:"search query"`
	TargetProperties []string `json:"targetProperties" jsonschema:"target properties"`
}

func (w *weaviateClient) Query(ctx context.Context, req *mcp.CallToolRequest, args queryArgs) (*mcp.CallToolResult, any, error) {
	hybrid := weaviate_graphql.HybridArgumentBuilder{}
	hybrid.WithQuery(args.Query)

	res, err := w.GraphQL().Get().
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
		return nil, nil, err
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, nil, fmt.Errorf("unmarshal query response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: string(b),
			},
		},
	}, nil, nil
}

func (w *weaviateClient) batchInsert(ctx context.Context, objs ...*models.Object) ([]models.ObjectsGetResponse, error) {
	resp, err := w.Batch().ObjectsBatcher().WithObjects(objs...).Do(ctx)
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
