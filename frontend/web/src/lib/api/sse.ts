export interface SSEEvent<T = unknown> {
  type: string
  data: T
}

export class SSEParseError extends Error {
  constructor(eventName: string, options?: ErrorOptions) {
    super(`Invalid JSON in SSE event "${eventName}"`, options)
    this.name = 'SSEParseError'
  }
}

/** Parse an SSE response incrementally; no assumption is made about chunk boundaries. */
export async function* parseSSEStream<T extends SSEEvent>(
  stream: ReadableStream<Uint8Array>,
): AsyncGenerator<T> {
  const reader = stream.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  let eventName = 'message'
  let dataLines: string[] = []
  let completed = false

  const dispatch = (): T | null => {
    if (dataLines.length === 0) {
      eventName = 'message'
      return null
    }
    const raw = dataLines.join('\n')
    const type = eventName
    eventName = 'message'
    dataLines = []
    try {
      return { type, data: JSON.parse(raw) } as T
    } catch (error) {
      throw new SSEParseError(type, { cause: error })
    }
  }

  function processLine(line: string): T | null {
    if (line.endsWith('\r')) line = line.slice(0, -1)
    if (line === '') return dispatch()
    if (line.startsWith(':')) return null

    const separator = line.indexOf(':')
    const field = separator === -1 ? line : line.slice(0, separator)
    let value = separator === -1 ? '' : line.slice(separator + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') eventName = value
    if (field === 'data') dataLines.push(value)
    return null
  }

  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })
      let newline = buffer.indexOf('\n')
      while (newline !== -1) {
        const event = processLine(buffer.slice(0, newline))
        buffer = buffer.slice(newline + 1)
        if (event) yield event
        newline = buffer.indexOf('\n')
      }
    }

    buffer += decoder.decode()
    if (buffer.length > 0) {
      const event = processLine(buffer)
      if (event) yield event
    }
    const finalEvent = dispatch()
    if (finalEvent) yield finalEvent
    completed = true
  } finally {
    if (!completed) {
      // The consumer stopped iterating before the stream ended: cancel the
      // underlying source (reader.read is abandoned). Loss of cancellation is
      // ignored so the original parser or stream error still propagates.
      try {
        await reader.cancel()
      } catch {
        /* preserve the original error */
      }
    }
    reader.releaseLock()
  }
}
