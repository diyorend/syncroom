import { useEffect, useRef, useState, useCallback } from "react";
import type { ServerEvent, ClientEvent, ChatMessage } from "../api/client";

// YouTube IFrame API types — the API loads globally via the script tag in
// index.html and attaches to window.YT. TypeScript doesn't know about it
// by default, so we declare the shape we actually use.
declare global {
  interface Window {
    YT: {
      Player: new (
        elementId: string,
        options: {
          videoId: string;
          playerVars?: Record<string, number>;
          events?: {
            onReady?: (event: { target: YTPlayer }) => void;
            onStateChange?: (event: { data: number; target: YTPlayer }) => void;
          };
        },
      ) => YTPlayer;
      PlayerState: { PLAYING: number; PAUSED: number; ENDED: number };
    };
    onYouTubeIframeAPIReady: () => void;
  }
}

interface YTPlayer {
  loadVideoById(videoId: string): void;
  playVideo(): void;
  pauseVideo(): void;
  seekTo(seconds: number, allowSeekAhead: boolean): void;
  getCurrentTime(): number;
  getPlayerState(): number;
  destroy(): void;
}

interface RoomState {
  members: string[];
  messages: ChatMessage[];
  videoUrl: string;
  isPlaying: boolean;
  isConnected: boolean;
}

interface StartState {
  pos: number;
  playing: boolean;
}

// extractYouTubeID pulls the video ID from a full YouTube URL or returns
// the string as-is if it's already just an ID (11 characters).
function extractYouTubeID(url: string): string {
  try {
    const u = new URL(url);
    return u.searchParams.get("v") ?? url;
  } catch {
    return url;
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
  });

  const ws = useRef<WebSocket | null>(null);
  const player = useRef<YTPlayer | null>(null);
  // clientID is assigned by the server and echoed back in OriginClientID.
  // We track the most recent one we've seen our own events return as, so
  // we can implement self-echo prevention.
  const myClientID = useRef<string>("");
  // suppressUntil: a timestamp, not a single-use flag. Programmatic
  // seekTo()+playVideo()/pauseVideo() can each independently trigger
  // onStateChange (e.g. BUFFERING then PLAYING), so a one-shot boolean
  // only swallows the first of those and treats the second as a real
  // user action — which re-broadcasts a fake play/pause and causes a
  // feedback loop between clients. A short time window covers the
  // whole sequence regardless of how many callbacks the player fires.
  const suppressUntil = useRef(0);
  const playerReady = useRef(false);
  // videoUrlRef tracks the video URL the *player* currently reflects. This
  // must be a ref, not React state read inside the WS onmessage closure —
  // that closure is captured once when the effect mounts (deps: [roomCode])
  // and would otherwise always compare against the stale initial value,
  // making every subsequent play/pause/seek event look like a "new video"
  // and destroy+recreate the player on every single sync message.
  const videoUrlRef = useRef("");
  // Queues a pending player creation if the YouTube IFrame API script
  // hasn't finished loading yet by the time we need it.
  const pendingInit = useRef<{
    videoId: string;
    startState?: StartState;
  } | null>(null);

  // send is stable across renders — it sends a ClientEvent over the WS.
  const send = useCallback((ev: ClientEvent) => {
    if (ws.current?.readyState === WebSocket.OPEN) {
      ws.current.send(JSON.stringify(ev));
    }
  }, []);

  // initPlayer creates a YouTube IFrame player inside the #yt-player div.
  // startState (if given) is applied once the new player reports ready —
  // seeking/playing a player that doesn't exist yet is a no-op, so we can't
  // just call applySync() right after construction.
  const initPlayer = useCallback(
    (videoId: string, startState?: StartState) => {
      if (player.current) {
        player.current.destroy();
      }
      playerReady.current = false;

      player.current = new window.YT.Player("yt-player", {
        videoId,
        playerVars: { autoplay: 0, controls: 1 },
        events: {
          onReady: () => {
            playerReady.current = true;
            if (startState) {
              suppressUntil.current = Date.now() + 1200;
              if (startState.pos > 1) {
                player.current?.seekTo(startState.pos, true);
              }
              if (startState.playing) {
                player.current?.playVideo();
              }
            }
          },
          onStateChange: (event) => {
            if (Date.now() < suppressUntil.current) return;
            const YT = window.YT.PlayerState;
            const pos = player.current?.getCurrentTime() ?? 0;

            if (event.data === YT.PLAYING) {
              send({ type: "play", position_seconds: pos });
            } else if (event.data === YT.PAUSED) {
              send({ type: "pause", position_seconds: pos });
            }
          },
        },
      });
    },
    [send],
  );

  // ensurePlayer creates the player now if the YouTube API is ready, or
  // queues the request to run once it finishes loading.
  const ensurePlayer = useCallback(
    (videoId: string, startState?: StartState) => {
      if (window.YT?.Player) {
        initPlayer(videoId, startState);
      } else {
        pendingInit.current = { videoId, startState };
        window.onYouTubeIframeAPIReady = () => {
          if (pendingInit.current) {
            initPlayer(
              pendingInit.current.videoId,
              pendingInit.current.startState,
            );
            pendingInit.current = null;
          }
        };
      }
    },
    [initPlayer],
  );

  // applySync applies a server sync event to the local player, computing
  // the live position from the snapshot-plus-elapsed-time pattern.
  const applySync = useCallback((ev: ServerEvent) => {
    if (!player.current || !playerReady.current) return;

    let pos = ev.position_seconds;
    if (ev.is_playing) {
      const elapsed =
        (Date.now() - new Date(ev.last_updated_at).getTime()) / 1000;
      pos += elapsed;
    }

    // Only hard-seek if we're actually off by enough to matter. Every
    // sync message forcing a seekTo was causing constant rebuffering,
    // which is itself a source of spurious onStateChange callbacks.
    const current = player.current.getCurrentTime();
    const drift = Math.abs(current - pos);

    // Cover the whole callback sequence this call can trigger (seek can
    // emit BUFFERING, playVideo/pauseVideo can emit PLAYING/PAUSED —
    // sometimes both, sometimes one), not just the next single event.
    suppressUntil.current = Date.now() + 1200;

    if (drift > 1.5) {
      player.current.seekTo(pos, true);
    }

    if (ev.is_playing) {
      player.current.playVideo();
    } else {
      player.current.pauseVideo();
    }
  }, []);
  // WebSocket lifecycle
  useEffect(() => {
    videoUrlRef.current = "";
    playerReady.current = false;

    const wsBase = import.meta.env.VITE_WS_URL || "ws://localhost:8080";
    const nameParam = encodeURIComponent(displayName);
    const tokenParam = token ? `&token=${token}` : "";
    const url = `${wsBase}/ws/${roomCode}?name=${nameParam}${tokenParam}`;

    const socket = new WebSocket(url);
    ws.current = socket;

    socket.onopen = () => {
      setState((s) => ({ ...s, isConnected: true }));
    };

    socket.onclose = () => {
      setState((s) => ({ ...s, isConnected: false }));
    };

    socket.onmessage = (rawMsg) => {
      let ev: ServerEvent;
      try {
        ev = JSON.parse(rawMsg.data);
      } catch {
        return;
      }

      switch (ev.type) {
        case "sync": {
          // your_client_id is sent exactly once, on the join snapshot — it's
          // the server telling us our own ID directly. Do NOT infer this
          // from origin_client_id on ordinary sync broadcasts; every other
          // client's broadcast has an origin_client_id too, and copying it
          // in here on every message made isSelfEcho trivially true forever
          // (comparing a value to the value you just assigned it), which is
          // why play/pause from other clients was silently ignored.
          if (ev.your_client_id) myClientID.current = ev.your_client_id;
          const isSelfEcho =
            !!ev.origin_client_id && ev.origin_client_id === myClientID.current;

          setState((s) => ({
            ...s,
            videoUrl: ev.video_url || s.videoUrl,
            isPlaying: ev.is_playing,
          }));

          const videoChanged =
            !!ev.video_url && ev.video_url !== videoUrlRef.current;
          if (videoChanged) videoUrlRef.current = ev.video_url;

          if (videoChanged) {
            // (Re)create the player for every client, including whoever set
            // the video — this is the one and only place the player gets
            // created, so there's no race between a locally-fired init and
            // the server echo arriving with the authoritative state.
            let pos = ev.position_seconds;
            if (ev.is_playing) {
              pos +=
                (Date.now() - new Date(ev.last_updated_at).getTime()) / 1000;
            }
            ensurePlayer(extractYouTubeID(ev.video_url), {
              pos,
              playing: ev.is_playing,
            });
            break;
          }

          // Same video — just a play/pause/seek update. Skip re-applying
          // our own action (avoids a feedback loop); apply everyone else's.
          if (!isSelfEcho) applySync(ev);
          break;
        }

        case "presence":
          setState((s) => ({ ...s, members: ev.members ?? [] }));
          break;

        case "chat":
          setState((s) => ({
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
          }));
          break;
      }
    };

    return () => {
      socket.close();
      player.current?.destroy();
      player.current = null;
    };
  }, [roomCode]); // only re-run if room code changes — not on every render

  const sendSetVideo = useCallback(
    (url: string) => {
      // Just send it — the player is (re)created uniformly for every client,
      // including us, when the server echoes the resulting sync event back.
      send({ type: "set_video", video_url: url });
    },
    [send],
  );

  const sendChat = useCallback(
    (body: string) => {
      if (body.trim()) send({ type: "chat", chat_body: body });
    },
    [send],
  );

  return { state, sendSetVideo, sendChat };
}
