import { useState, useEffect, useRef } from "react";
import { Toaster, toast } from "react-hot-toast";
import { login, register, createRoom, getRoom } from "./api/client";
import type { Room, ChatMessage } from "./api/client";
import { useRoom } from "./hooks/useRoom";

// ---- styles (all inline, no CSS file needed for a portfolio project) ----

const s = {
  page: {
    fontFamily: "system-ui, sans-serif",
    maxWidth: 960,
    margin: "0 auto",
    padding: 24,
    color: "#111",
  } as const,
  card: {
    background: "#fff",
    border: "1px solid #e5e7eb",
    borderRadius: 8,
    padding: 20,
  } as const,
  input: {
    width: "100%",
    padding: "10px 12px",
    border: "1px solid #d1d5db",
    borderRadius: 6,
    fontSize: 14,
    boxSizing: "border-box" as const,
    marginBottom: 10,
  },
  btn: (color = "#2563eb") => ({
    padding: "10px 18px",
    background: color,
    color: "#fff",
    border: "none",
    borderRadius: 6,
    fontSize: 14,
    cursor: "pointer",
    fontWeight: 600,
  }),
  tag: {
    display: "inline-block",
    background: "#eff6ff",
    color: "#1d4ed8",
    padding: "2px 8px",
    borderRadius: 9999,
    fontSize: 12,
    marginRight: 4,
    marginBottom: 4,
  },
};

// ---- Auth screen ----

function AuthScreen({
  onAuth,
  onGuestJoin,
}: {
  onAuth: (token: string, email: string) => void;
  onGuestJoin: (name: string, code: string) => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [registering, setRegistering] = useState(false);

  const [guestName, setGuestName] = useState("");
  const [guestCode, setGuestCode] = useState("");
  const [guestLoading, setGuestLoading] = useState(false);

  const submit = async () => {
    try {
      const fn = registering ? register : login;
      const res = await fn(email, password);
      onAuth(res.data.token, res.data.user.email);
    } catch (e: unknown) {
      const msg =
        (e as { response?: { data?: { error?: string } } }).response?.data
          ?.error ?? "Failed";
      toast.error(msg);
    }
  };

  const submitGuest = async () => {
    if (!guestName.trim() || !guestCode.trim()) return;
    setGuestLoading(true);
    try {
      await onGuestJoin(guestName.trim(), guestCode.trim().toUpperCase());
    } finally {
      setGuestLoading(false);
    }
  };

  return (
    <div
      style={{
        maxWidth: 400,
        margin: "80px auto",
        display: "flex",
        flexDirection: "column",
        gap: 16,
      }}
    >
      <div style={s.card}>
        <h2 style={{ marginTop: 0 }}>SyncRoom</h2>
        <p style={{ color: "#6b7280", marginBottom: 20 }}>
          Watch YouTube videos in sync with anyone.
        </p>
        <input
          style={s.input}
          placeholder="Email"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
        />
        <input
          style={s.input}
          type="password"
          placeholder="Password (min 8 chars)"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
        />
        <button
          style={{ ...s.btn(), width: "100%", marginBottom: 8 }}
          onClick={submit}
        >
          {registering ? "Register" : "Login"}
        </button>
        <button
          style={{
            ...s.btn("#6b7280"),
            width: "100%",
            background: "none",
            color: "#6b7280",
          }}
          onClick={() => setRegistering(!registering)}
        >
          {registering
            ? "Already have an account? Login"
            : "No account? Register"}
        </button>
      </div>
      <div style={s.card}>
        <h3 style={{ marginTop: 0 }}>Or join as a guest</h3>
        <p style={{ color: "#6b7280" }}>
          No account needed — just enter a display name and the room code
          someone shared with you.
        </p>
        <input
          style={s.input}
          placeholder="Your display name"
          value={guestName}
          onChange={(e) => setGuestName(e.target.value)}
        />
        <input
          style={s.input}
          placeholder="Room code (e.g. BLUE-FOX-42)"
          value={guestCode}
          onChange={(e) => setGuestCode(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submitGuest()}
        />
        <button
          style={{ ...s.btn("#7c3aed"), width: "100%" }}
          onClick={submitGuest}
          disabled={guestLoading}
        >
          {guestLoading ? "Joining..." : "Join as Guest"}
        </button>
      </div>
    </div>
  );
}

// ---- Lobby screen ----

function LobbyScreen({
  email,
  onJoin,
}: {
  email: string;
  onJoin: (code: string, room: Room, msgs: ChatMessage[]) => void;
}) {
  const [joinCode, setJoinCode] = useState("");
  const [loading, setLoading] = useState(false);

  const handleCreate = async () => {
    setLoading(true);
    try {
      const res = await createRoom();
      const roomRes = await getRoom(res.data.code);
      onJoin(res.data.code, roomRes.data.room, roomRes.data.messages);
    } catch {
      toast.error("Failed to create room");
    } finally {
      setLoading(false);
    }
  };

  const handleJoin = async () => {
    const code = joinCode.trim().toUpperCase();
    if (!code) return;
    setLoading(true);
    try {
      const res = await getRoom(code);
      onJoin(code, res.data.room, res.data.messages);
    } catch {
      toast.error("Room not found");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div style={{ maxWidth: 500, margin: "80px auto" }}>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 24,
        }}
      >
        <h2 style={{ margin: 0 }}>SyncRoom</h2>
        <span style={{ color: "#6b7280", fontSize: 13 }}>{email}</span>
      </div>
      <div style={s.card}>
        <h3 style={{ marginTop: 0 }}>Create a room</h3>
        <p style={{ color: "#6b7280" }}>
          Start a new watch party. Share the room code with anyone.
        </p>
        <button style={s.btn()} onClick={handleCreate} disabled={loading}>
          {loading ? "Creating..." : "Create Room"}
        </button>
      </div>
      <div style={{ ...s.card, marginTop: 16 }}>
        <h3 style={{ marginTop: 0 }}>Join a room</h3>
        <input
          style={s.input}
          placeholder="Room code (e.g. BLUE-FOX-42)"
          value={joinCode}
          onChange={(e) => setJoinCode(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && handleJoin()}
        />
        <button
          style={s.btn("#059669")}
          onClick={handleJoin}
          disabled={loading}
        >
          {loading ? "Joining..." : "Join Room"}
        </button>
      </div>
    </div>
  );
}

// ---- Chat panel ----

function ChatPanel({
  messages,
  onSend,
}: {
  messages: ChatMessage[];
  onSend: (body: string) => void;
}) {
  const [text, setText] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%" }}>
      <div
        style={{
          flex: 1,
          overflowY: "auto",
          padding: "0 4px",
          marginBottom: 8,
        }}
      >
        {messages.map((m) => (
          <div key={m.id} style={{ marginBottom: 8 }}>
            <strong style={{ fontSize: 12, color: "#6b7280" }}>
              {m.sender_name}
            </strong>
            <div style={{ fontSize: 14 }}>{m.body}</div>
          </div>
        ))}
        <div ref={bottomRef} />
      </div>
      <div style={{ display: "flex", gap: 8 }}>
        <input
          style={{ ...s.input, marginBottom: 0, flex: 1 }}
          placeholder="Message..."
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              onSend(text);
              setText("");
            }
          }}
        />
        <button
          style={s.btn()}
          onClick={() => {
            onSend(text);
            setText("");
          }}
        >
          Send
        </button>
      </div>
    </div>
  );
}

// ---- Room screen ----

function RoomScreen({
  roomCode,
  room,
  initialMessages,
  token,
  email,
  onLeave,
}: {
  roomCode: string;
  room: Room;
  initialMessages: ChatMessage[];
  token: string | null;
  email: string;
  onLeave: () => void;
}) {
  const [videoInput, setVideoInput] = useState(room.video_url || "");
  const { state, sendSetVideo, sendChat } = useRoom(
    roomCode,
    email,
    token,
    initialMessages,
    room.video_url || "",
  );

  return (
    <div style={s.page}>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 16,
        }}
      >
        <div>
          <strong>Room: {roomCode}</strong>
          <span style={{ marginLeft: 12, ...s.tag }}>
            {state.isConnected ? "● Connected" : "○ Connecting..."}
          </span>
        </div>
        <button style={s.btn("#6b7280")} onClick={onLeave}>
          Leave Room
        </button>
      </div>

      <div
        style={{
          display: "grid",
          gridTemplateColumns: "1fr 320px",
          gap: 16,
          height: 540,
        }}
      >
        {/* Video panel */}
        <div style={{ display: "flex", flexDirection: "column" }}>
          {/* YouTube IFrame Player — this outer box is always visible and
              sized (never display:none), so the YT API always attaches to a
              properly-dimensioned element. The "no video yet" message is an
              overlay on top rather than a sibling that swaps places with it. */}
          <div
            style={{
              position: "relative",
              width: "100%",
              aspectRatio: "16/9",
              background: "#000",
              borderRadius: 8,
              overflow: "hidden",
            }}
          >
            <div id="yt-player" style={{ width: "100%", height: "100%" }} />
            {!state.videoUrl && (
              <div
                style={{
                  position: "absolute",
                  inset: 0,
                  display: "flex",
                  alignItems: "center",
                  justifyContent: "center",
                }}
              >
                <div style={{ textAlign: "center", color: "#9ca3af" }}>
                  <div style={{ fontSize: 32, marginBottom: 8 }}>▶</div>
                  <div>Paste a YouTube URL below to start watching</div>
                </div>
              </div>
            )}
          </div>
          <div style={{ display: "flex", gap: 8, marginTop: 10 }}>
            <input
              style={{ ...s.input, marginBottom: 0, flex: 1 }}
              placeholder="YouTube URL (e.g. https://youtube.com/watch?v=...)"
              value={videoInput}
              onChange={(e) => setVideoInput(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && sendSetVideo(videoInput)}
            />
            <button
              style={s.btn("#059669")}
              onClick={() => sendSetVideo(videoInput)}
            >
              Set
            </button>
          </div>
        </div>

        {/* Sidebar: presence + chat */}
        <div
          style={{
            display: "flex",
            flexDirection: "column",
            gap: 12,
            height: "100%",
          }}
        >
          <div style={s.card}>
            <strong style={{ fontSize: 13 }}>In this room</strong>
            <div style={{ marginTop: 8 }}>
              {state.members.length === 0 ? (
                <span style={{ color: "#9ca3af", fontSize: 13 }}>
                  Just you...
                </span>
              ) : (
                state.members.map((name) => (
                  <span key={name} style={s.tag}>
                    {name}
                  </span>
                ))
              )}
            </div>
          </div>
          <div
            style={{
              ...s.card,
              flex: 1,
              display: "flex",
              flexDirection: "column",
              overflow: "hidden",
            }}
          >
            <strong style={{ fontSize: 13, marginBottom: 8, display: "block" }}>
              Chat
            </strong>
            <div style={{ flex: 1, overflow: "hidden" }}>
              <ChatPanel messages={state.messages} onSend={sendChat} />
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---- Root ----

type Screen = "auth" | "lobby" | "room";

export default function App() {
  const [screen, setScreen] = useState<Screen>(() =>
    localStorage.getItem("token") ? "lobby" : "auth",
  );
  const [token, setToken] = useState<string | null>(() =>
    localStorage.getItem("token"),
  );
  const [email, setEmail] = useState(() => localStorage.getItem("email") || "");
  const [activeRoom, setActiveRoom] = useState<{
    code: string;
    room: Room;
    messages: ChatMessage[];
  } | null>(null);

  // On page load, if we were in a room before refresh, silently rejoin it
  // (re-fetch its current state) instead of dumping the user back to the
  // lobby/auth screen. This runs once on mount.
  useEffect(() => {
    const savedCode = localStorage.getItem("activeRoomCode");
    if (!savedCode) return;
    const savedGuestName = localStorage.getItem("guestName");

    (async () => {
      try {
        const res = await getRoom(savedCode);
        if (savedGuestName) setEmail(savedGuestName);
        setActiveRoom({
          code: savedCode,
          room: res.data.room,
          messages: res.data.messages,
        });
        setScreen("room");
      } catch {
        localStorage.removeItem("activeRoomCode");
        localStorage.removeItem("guestName");
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleAuth = (t: string, e: string) => {
    localStorage.setItem("token", t);
    localStorage.setItem("email", e);
    setToken(t);
    setEmail(e);
    setScreen("lobby");
  };

  const handleJoin = (code: string, room: Room, messages: ChatMessage[]) => {
    localStorage.setItem("activeRoomCode", code);
    setActiveRoom({ code, room, messages });
    setScreen("room");
  };

  const handleGuestJoin = async (name: string, code: string) => {
    try {
      const res = await getRoom(code);
      localStorage.setItem("activeRoomCode", code);
      localStorage.setItem("guestName", name);
      setEmail(name);
      setActiveRoom({ code, room: res.data.room, messages: res.data.messages });
      setScreen("room");
    } catch {
      toast.error("Room not found");
    }
  };

  const handleLeave = () => {
    localStorage.removeItem("activeRoomCode");
    localStorage.removeItem("guestName");
    setActiveRoom(null);
    setScreen(token ? "lobby" : "auth");
  };

  return (
    <>
      <Toaster position="top-right" />
      {screen === "auth" && (
        <AuthScreen onAuth={handleAuth} onGuestJoin={handleGuestJoin} />
      )}
      {screen === "lobby" && <LobbyScreen email={email} onJoin={handleJoin} />}
      {screen === "room" && activeRoom && (
        <RoomScreen
          roomCode={activeRoom.code}
          room={activeRoom.room}
          initialMessages={activeRoom.messages}
          token={token}
          email={email}
          onLeave={handleLeave}
        />
      )}
    </>
  );
}
