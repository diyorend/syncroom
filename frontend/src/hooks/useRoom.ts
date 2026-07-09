import { useEffect, useRef, useState, useCallback } from 'react'
import type { ServerEvent, ClientEvent, ChatMessage } from '../api/client'

// YouTube IFrame API types — the API loads globally via the script tag in
// index.html and attaches to window.YT. TypeScript doesn't know about it
// by default, so we declare the shape we actually use.
declare global {
  interface Window {
    YT: {
      Player: new (
        elementId: string,
        options: {
          videoId: string
          playerVars?: Record<string, number>
          events?: {
            onReady?: (event: { target: YTPlayer }) => void
            onStateChange?: (event: { data: number; target: YTPlayer }) => void
          }
        }
      ) => YTPlayer
      PlayerState: { PLAYING: number; PAUSED: number; ENDED: number }
    }
    onYouTubeIframeAPIReady: () => void
  }
}

interface YTPlayer {
  loadVideoById(videoId: string): void
  playVideo(): void
  pauseVideo(): void
  seekTo(seconds: number, allowSeekAhead: boolean): void
  getCurrentTime(): number
  getPlayerState(): number
  destroy(): void
}

interface RoomState {
  members: string[]
  messages: ChatMessage[]
  videoUrl: string
  isPlaying: boolean
  isConnected: boolean
}

// extractYouTubeID pulls the video ID from a full YouTube URL or returns
// the string as-is if it's already just an ID (11 characters).
function extractYouTubeID(url: string): string {
  try {
    const u = new URL(url)
    return u.searchParams.get('v') ?? url
  } catch {
    return url
  }
}

export function useRoom(
  roomCode: string,
  displayName: string,
  token: string | null,
  initialMessages: ChatMessage[],
  initialVideoUrl: string,
) {
  const [state, setState] = useState<RoomState>({
    members: [],
    messages: initialMessages,
    videoUrl: initialVideoUrl,
    isPlaying: false,
    isConnected: false,
  })

  const ws = useRef<WebSocket | null>(null)
  const player = useRef<YTPlayer | null>(null)
  // clientID is assigned by the server and echoed back in OriginClientID.
  // We track the most recent one we've seen our own events return as, so
  // we can implement self-echo prevention.
  const myClientID = useRef<string>('')
  // ignoreNextStateChange: true when we programmatically control the player
  // (from a sync event) so the player's own stateChange callback doesn't
  // re-emit another event back to the room. This prevents the ping-pong loop.
  const ignoreNextStateChange = useRef(false)
  const playerReady = useRef(false)

  // send is stable across renders — it sends a ClientEvent over the WS.
  const send = useCallback((ev: ClientEvent) => {
    if (ws.current?.readyState === WebSocket.OPEN) {
      ws.current.send(JSON.stringify(ev))
    }
  }, [])

  // initPlayer creates a YouTube IFrame player inside the #yt-player div.
  // Called when we first get a video URL (either from the initial room state
  // or from a set_video event).
  const initPlayer = useCallback((videoId: string) => {
    if (!window.YT?.Player) return

    if (player.current) {
      player.current.destroy()
    }

    player.current = new window.YT.Player('yt-player', {
      videoId,
      playerVars: { autoplay: 0, controls: 1 },
      events: {
        onReady: () => {
          playerReady.current = true
        },
        onStateChange: (event) => {
          if (ignoreNextStateChange.current) {
            ignoreNextStateChange.current = false
            return
          }
          const YT = window.YT.PlayerState
          const pos = player.current?.getCurrentTime() ?? 0

          if (event.data === YT.PLAYING) {
            send({ type: 'play', position_seconds: pos })
          } else if (event.data === YT.PAUSED) {
            send({ type: 'pause', position_seconds: pos })
          }
        },
      },
    })
  }, [send])

  // applySync applies a server sync event to the local player, computing
  // the live position from the snapshot-plus-elapsed-time pattern.
  const applySync = useCallback((ev: ServerEvent) => {
    if (!player.current || !playerReady.current) return

    let pos = ev.position_seconds
    if (ev.is_playing) {
      const elapsed = (Date.now() - new Date(ev.last_updated_at).getTime()) / 1000
      pos += elapsed
    }

    ignoreNextStateChange.current = true
    player.current.seekTo(pos, true)

    if (ev.is_playing) {
      player.current.playVideo()
    } else {
      player.current.pauseVideo()
    }
  }, [])

  // WebSocket lifecycle
  useEffect(() => {
    const wsBase = import.meta.env.VITE_WS_URL || 'ws://localhost:8080'
    const nameParam = encodeURIComponent(displayName)
    const tokenParam = token ? `&token=${token}` : ''
    const url = `${wsBase}/ws/${roomCode}?name=${nameParam}${tokenParam}`

    const socket = new WebSocket(url)
    ws.current = socket

    socket.onopen = () => {
      setState(s => ({ ...s, isConnected: true }))
    }

    socket.onclose = () => {
      setState(s => ({ ...s, isConnected: false }))
    }

    socket.onmessage = (rawMsg) => {
      let ev: ServerEvent
      try {
        ev = JSON.parse(rawMsg.data)
      } catch {
        return
      }

      switch (ev.type) {
        case 'sync': {
          // Track the origin_client_id we get back from our own events so
          // we know what our own ID is. Then skip applying sync events we
          // ourselves originated — the player already reflects our intent.
          if (ev.origin_client_id) myClientID.current = ev.origin_client_id
          if (ev.origin_client_id === myClientID.current) break

          setState(s => ({
            ...s,
            videoUrl: ev.video_url || s.videoUrl,
            isPlaying: ev.is_playing,
          }))

          if (ev.video_url && ev.video_url !== state.videoUrl) {
            const vidId = extractYouTubeID(ev.video_url)
            initPlayer(vidId)
          }
          applySync(ev)
          break
        }

        case 'presence':
          setState(s => ({ ...s, members: ev.members ?? [] }))
          break

        case 'chat':
          setState(s => ({
            ...s,
            messages: [
              ...s.messages,
              {
                id: crypto.randomUUID(),
                room_id: roomCode,
                sender_name: ev.sender_name,
                body: ev.chat_body,
                created_at: new Date().toISOString(),
              },
            ],
          }))
          break
      }
    }

    // Initialize the YouTube player once the API is ready, if we already
    // have a video URL from the initial room state.
    if (initialVideoUrl) {
      const initWhenReady = () => initPlayer(extractYouTubeID(initialVideoUrl))
      if (window.YT?.Player) {
        initWhenReady()
      } else {
        window.onYouTubeIframeAPIReady = initWhenReady
      }
    } else {
      window.onYouTubeIframeAPIReady = () => { /* player created on first set_video event */ }
    }

    return () => {
      socket.close()
      player.current?.destroy()
    }
  }, [roomCode]) // only re-run if room code changes — not on every render

  const sendSetVideo = useCallback((url: string) => {
    send({ type: 'set_video', video_url: url })
    const vidId = extractYouTubeID(url)
    if (window.YT?.Player) {
      initPlayer(vidId)
    } else {
      window.onYouTubeIframeAPIReady = () => initPlayer(vidId)
    }
  }, [send, initPlayer])

  const sendChat = useCallback((body: string) => {
    if (body.trim()) send({ type: 'chat', chat_body: body })
  }, [send])

  return { state, sendSetVideo, sendChat }
}
