// Command stdio-server is an MCP fixture used by internal/mcp's stdio
// transport tests. Speaks JSON-RPC over stdin/stdout via mcp-go's
// server.ServeStdio. Exports two tools:
//   - echo(text string)   → returns text
//   - add(a, b float64)   → returns the sum as a string
//
// Flags:
//
//	--spawn-child    spawn a long-sleep child before serving, so
//	                 tree-kill tests can observe both processes.
//	--stderr-line=S  write S to stderr before serving, so stderr-
//	                 passthrough tests can observe a line.
//	--hang-init=DUR  block the echo tool handler for DUR before
//	                 responding, so context-cancel tests can race.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	spawnChild := flag.Bool("spawn-child", false, "spawn a long-sleep child before serving")
	stderrLine := flag.String("stderr-line", "", "write S to stderr before serving")
	hangInit := flag.Duration("hang-init", 0, "block echo tool handler for DUR")
	flag.Parse()

	if *stderrLine != "" {
		fmt.Fprintln(os.Stderr, *stderrLine)
	}

	if *spawnChild {
		spawnSleepChild()
	}

	srv := server.NewMCPServer("fixture", "0.0.1")

	echoTool := mcp.NewTool("echo",
		mcp.WithDescription("Echo the input text"),
		mcp.WithString("text", mcp.Required(), mcp.Description("text to echo")),
	)
	srv.AddTool(echoTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if *hangInit > 0 {
			select {
			case <-time.After(*hangInit):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		text, err := req.RequireString("text")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(text), nil
	})

	addTool := mcp.NewTool("add",
		mcp.WithDescription("Add two numbers"),
		mcp.WithNumber("a", mcp.Required()),
		mcp.WithNumber("b", mcp.Required()),
	)
	srv.AddTool(addTool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, err := req.RequireFloat("a")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		b, err := req.RequireFloat("b")
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(strconv.FormatFloat(a+b, 'f', -1, 64)), nil
	})

	if err := server.ServeStdio(srv); err != nil {
		fmt.Fprintf(os.Stderr, "fixture: serve: %v\n", err)
		os.Exit(1)
	}
}
