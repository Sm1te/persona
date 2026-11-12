// A guest's declared Persona tools through the Agent SDK's in-process MCP
// transport. The small stdio JSON-RPC client below adds no MCP dependency.
import { spawn } from 'node:child_process'
import { createInterface } from 'node:readline'

export async function guestMcpServer(config) {
  const child = spawn(config.command, config.args ?? [], {
    env: { ...process.env, ...(config.env ?? {}) },
    stdio: ['pipe', 'pipe', 'pipe'],
  })
  const pending = new Map()
  const failed = []
  let nextID = 0
  let closed = false
  const lines = createInterface({ input: child.stdout, crlfDelay: Infinity })
  lines.on('line', (line) => {
    let message
    try { message = JSON.parse(line) } catch { return }
    const waiter = pending.get(message.id)
    if (!waiter) return
    pending.delete(message.id)
    if (message.error) waiter.reject(new Error(message.error.message ?? 'Persona MCP error'))
    else waiter.resolve(message.result)
  })
  child.stderr.on('data', () => {}) // diagnostics never enter the protocol or model context
  child.on('error', (err) => {
    for (const waiter of pending.values()) waiter.reject(err)
    pending.clear()
  })
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    if (closed) { reject(new Error('Persona MCP is closed')); return }
    const id = ++nextID
    pending.set(id, { resolve, reject })
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', id, method, params })}\n`, (err) => {
      if (err) { pending.delete(id); reject(err) }
    })
  })
  const close = async () => {
    if (closed) return
    closed = true
    for (const waiter of pending.values()) waiter.reject(new Error('Persona MCP closed'))
    pending.clear()
    child.stdin.end()
    child.kill()
    await new Promise((resolve) => { child.once('exit', resolve); setTimeout(resolve, 1000).unref() })
  }
  try {
    await call('initialize', {
      protocolVersion: '2025-11-25', capabilities: {},
      clientInfo: { name: 'persona-guest-bridge', version: '1.0.0' },
    })
    child.stdin.write(`${JSON.stringify({ jsonrpc: '2.0', method: 'notifications/initialized' })}\n`)
    const { tools } = await call('tools/list')
    if (tools.length === 0) throw new Error('the guest MCP server advertised no tools')
    const instance = {
      async connect(transport) {
        transport.onmessage = async (message) => {
          if (!message || message.jsonrpc !== '2.0' || message.id === undefined || message.id === null) return
          const reply = (result) => transport.send({ jsonrpc: '2.0', id: message.id, result })
          if (message.method === 'initialize') {
            await reply({
              protocolVersion: '2025-11-25', capabilities: { tools: { listChanged: false } },
              serverInfo: { name: 'persona', version: '1.0.0' },
            })
          } else if (message.method === 'tools/list') {
            await reply({ tools })
          } else if (message.method === 'tools/call') {
            try {
              await reply(await call('tools/call', {
                name: message.params?.name,
                arguments: message.params?.arguments ?? {},
              }))
            } catch (err) {
              await reply({ content: [{ type: 'text', text: err?.message ?? String(err) }], isError: true })
            }
          } else if (message.method === 'ping') {
            await reply({})
          } else {
            await transport.send({ jsonrpc: '2.0', id: message.id, error: { code: -32601, message: 'Method not found' } })
          }
        }
      },
      async close() { await close() },
    }
    return { server: { type: 'sdk', name: 'persona', instance }, close }
  } catch (err) {
    await close().catch(() => {})
    throw err
  }
}
