// A guest MCP server used by guest-mcp.test.mjs: only the declared tool.
import { createInterface } from 'node:readline'
const lines = createInterface({ input: process.stdin, crlfDelay: Infinity })
for await (const line of lines) {
  const msg = JSON.parse(line)
  if (msg.method === 'initialize') {
    console.log(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result: {
      protocolVersion: '2025-03-26', capabilities: { tools: { listChanged: false } },
      serverInfo: { name: 'persona-test', version: '1.0.0' }, instructions: 'Only the declared tool.',
    } }))
  } else if (msg.method === 'notifications/initialized') {
    continue
  } else if (msg.method === 'tools/list') {
    console.log(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result: { tools: process.env.EMPTY_TOOLS ? [] : [{
      name: 'exampledocs_search', description: 'Read synthetic docs.',
      inputSchema: { type: 'object', properties: { query: { type: 'string' } }, required: ['query'] },
    }] } }))
  } else if (msg.method === 'tools/call') {
    console.log(JSON.stringify({ jsonrpc: '2.0', id: msg.id, result: {
      content: [{ type: 'text', text: `synthetic:${msg.params.arguments.query}` }], isError: false,
    } }))
  }
}
