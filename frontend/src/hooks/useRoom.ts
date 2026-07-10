import { useEffect, useRef, useState, useCallback } from "react";
import type { ServerEvent, ClientEvent, ChatMessage } from "../api/client";

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

  const myClientID = useRef<string>("");

  const suppressUntil = useRef(0);
  const playerReady = useRef(false);

  const videoUrlRef = useRef("");

  const pendingInit = useRef<{
    videoId: string;
    startState?: StartState;
  } | null>(null);

  const send = useCallback((ev: ClientEvent) => {
    if (ws.current?.readyState === WebSocket.OPEN) {
      ws.current.send(JSON.stringify(ev));
    }
  }, []);

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

  const applySync = useCallback((ev: ServerEvent) => {
    if (!player.current || !playerReady.current) return;

    let pos = ev.position_seconds;
    if (ev.is_playing) {
      const elapsed =
        (Date.now() - new Date(ev.last_updated_at).getTime()) / 1000;
      pos += elapsed;
    }

    const current = player.current.getCurrentTime();
    const drift = Math.abs(current - pos);

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
  }, [roomCode]);

  const sendSetVideo = useCallback(
    (url: string) => {
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
