import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { CallToolRequestSchema, ListToolsRequestSchema } from "@modelcontextprotocol/sdk/types.js";

const server = new Server({ name: "stdio-fixture", version: "1" }, { capabilities: { tools: {} } });
server.setRequestHandler(ListToolsRequestSchema, async () => ({
	tools: [{ name: "echo", description: "Echo configuration", inputSchema: { type: "object" } }],
}));
server.setRequestHandler(CallToolRequestSchema, async (request) => ({
	content: [
		{
			type: "text",
			text: request.params.arguments?.fail
				? "Expected fixture error"
				: `${process.env.MCP_FIXTURE}:${process.cwd()}`,
		},
	],
	isError: request.params.arguments?.fail === true,
}));
await server.connect(new StdioServerTransport());
