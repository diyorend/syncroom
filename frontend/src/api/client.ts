import axios from 'axios'

const api = axios.create({
  baseURL: import.meta.env.VITE_API_URL || 'http://localhost:8080',
})

api.interceptors.request.use((config) => {
  const token = localStorage.getItem('token')
  if (token) config.headers.Authorization = `Bearer ${token}`
  return config
})

api.interceptors.response.use(
  (r) => r,
  (error) => {
    if (error.response?.status === 401) {
      localStorage.removeItem('token')
      window.location.href = '/'
    }
    return Promise.reject(error)
  }
)

export default api

// --- Types ---

export interface User {
  id: string
  email: string
}

export interface Room {
  id: string
  code: string
  creator_id: string
  video_url: string
  created_at: string
}

export interface ChatMessage {
  id: string
  room_id: string
  sender_name: string
  body: string
  created_at: string
}

export type EventType = 'play' | 'pause' | 'seek' | 'chat' | 'sync' | 'presence' | 'set_video'

export interface ServerEvent {
  type: EventType
  position_seconds: number
  is_playing: boolean
  video_url: string
  last_updated_at: string
  sender_name: string
  chat_body: string
  origin_client_id: string
  members: string[]
}

export interface ClientEvent {
  type: EventType
  position_seconds?: number
  video_url?: string
  chat_body?: string
}

// --- API calls ---

export const register = (email: string, password: string) =>
  api.post<{ token: string; user: User }>('/api/auth/register', { email, password })

export const login = (email: string, password: string) =>
  api.post<{ token: string; user: User }>('/api/auth/login', { email, password })

export const createRoom = () =>
  api.post<Room>('/api/rooms')

export const getRoom = (code: string) =>
  api.get<{ room: Room; messages: ChatMessage[] }>(`/api/rooms/${code}`)
