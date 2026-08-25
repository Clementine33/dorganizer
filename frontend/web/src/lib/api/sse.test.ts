import { describe, expect, it } from 'vitest'
import { parseSSEStream } from './sse'
import type { ScanEvent } from './types'

function streamFromChunks(chunks: string[]): ReadableStream<Uint8Array> {
  const encoder = new TextEncoder()
  return new ReadableStream({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk))
      controller.close()
    },
  })
}

describe('parseSSEStream', () => {
  it('parses typed events when chunks split in the middle of a line', async () => {
    const stream = streamFromChunks([
      'event: started\ndata: {"stage":"scan","mess',
      'age":"Scanning /music"}\n\nevent: progress\ndata: {"stage":"scan",',
      '"files_scanned":12,"dirs_scanned":3}\n\nevent: completed\ndata: {"stage":"scan","scan_id":"scan-1",',
      '"root_path":"C:\\\\Music","files_scanned":12}\n\n',
    ])

    const events: ScanEvent[] = []
    for await (const event of parseSSEStream<ScanEvent>(stream)) events.push(event)

    expect(events).toEqual([
      { type: 'started', data: { stage: 'scan', message: 'Scanning /music' } },
      { type: 'progress', data: { stage: 'scan', files_scanned: 12, dirs_scanned: 3 } },
      {
        type: 'completed',
        data: { stage: 'scan', scan_id: 'scan-1', root_path: 'C:\\Music', files_scanned: 12 },
      },
    ])
  })

  it('supports CRLF, comments, and a final event without a blank terminator', async () => {
    const stream = streamFromChunks([
      ': heartbeat\r\nevent: cancelled\r\ndata: {"stage":"scan",',
      '"message":"scan canceled"}',
    ])

    const events: ScanEvent[] = []
    for await (const event of parseSSEStream<ScanEvent>(stream)) events.push(event)

    expect(events).toEqual([
      { type: 'cancelled', data: { stage: 'scan', message: 'scan canceled' } },
    ])
  })

  it('surfaces malformed event data with the event name', async () => {
    const stream = streamFromChunks(['event: error\ndata: not-json\n\n'])

    await expect(async () => {
      for await (const _event of parseSSEStream<ScanEvent>(stream)) {
        // consume the stream
      }
    }).rejects.toThrow('Invalid JSON in SSE event "error"')
  })

  it('cancels the underlying source when the iterator is closed before the stream ends', async () => {
    const encoder = new TextEncoder()
    let cancelled = 0
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(encoder.encode('event: started\ndata: {"stage":"scan","message":"hi"}\n\n'))
      },
      cancel(_reason: unknown) {
        cancelled += 1
      },
    })

    const generator = parseSSEStream<ScanEvent>(stream)
    const first = await generator.next()
    expect(first.value).toEqual({ type: 'started', data: { stage: 'scan', message: 'hi' } })

    // Break out of iteration while the stream is still open.
    await generator.return(undefined as never)
    expect(cancelled).toBe(1)
  })

  it('surfaces errors raised by the response stream', async () => {
    const failure = new Error('connection reset')
    const stream = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.error(failure)
      },
    })

    await expect(async () => {
      for await (const _event of parseSSEStream<ScanEvent>(stream)) {
        // consume the stream
      }
    }).rejects.toBe(failure)
  })
})
