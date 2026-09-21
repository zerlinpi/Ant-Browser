import { api } from "@/api/client";
import type { NotificationItem } from "@/types";

interface SocketTicket {
  ticket: string;
  expiresAt?: string;
}

export class NotificationChannel {
  private socket?: WebSocket;
  private retryTimer?: number;
  private closed = false;
  private retryDelay = 1_000;

  constructor(
    private readonly apiBaseURL: string,
    private readonly workspaceId: string,
    private readonly onMessage: (item: NotificationItem) => void,
  ) {}

  async connect() {
    this.closed = false;
    window.clearTimeout(this.retryTimer);
    try {
      const { ticket } = await api.post<SocketTicket>(`/api/v1/workspaces/${this.workspaceId}/notifications/socket-ticket`);
      const url = new URL("/api/v1/notifications/ws", this.apiBaseURL);
      url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
      const socket = new WebSocket(url, ["ant-browser.notification.v1", ticket]);
      this.socket = socket;
      socket.onopen = () => { this.retryDelay = 1_000; };
      socket.onmessage = (event) => {
        try {
          const envelope = JSON.parse(String(event.data));
          this.onMessage((envelope.data ?? envelope) as NotificationItem);
        } catch {
          // Ignore malformed third-party frames; the server only sends JSON.
        }
      };
      socket.onclose = () => this.scheduleReconnect();
      socket.onerror = () => socket.close();
    } catch {
      this.scheduleReconnect();
    }
  }

  private scheduleReconnect() {
    if (this.closed) return;
    window.clearTimeout(this.retryTimer);
    this.retryTimer = window.setTimeout(() => void this.connect(), this.retryDelay);
    this.retryDelay = Math.min(this.retryDelay * 2, 30_000);
  }

  close() {
    this.closed = true;
    window.clearTimeout(this.retryTimer);
    this.socket?.close();
    this.socket = undefined;
  }
}
