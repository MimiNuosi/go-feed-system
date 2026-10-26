import { defineStore } from "pinia";
import {
  api,
  getRefreshToken,
  getToken,
  setRefreshToken,
  setToken,
  type MessageItem,
  type User,
} from "./api";

interface ToastItem {
  id: number;
  text: string;
  kind: "info" | "error";
}

export const useAuthStore = defineStore("auth", {
  state: () => ({
    token: getToken(),
    user: null as User | null,
    loading: false,
  }),
  getters: {
    isLoggedIn: (state) => Boolean(state.token),
  },
  actions: {
    async restore() {
      if (!this.token) {
        return;
      }
      try {
        this.user = await api.me();
      } catch {
        this.logout();
      }
    },
    async login(email: string, password: string) {
      const result = await api.login(email, password);
      this.token = result.access_token;
      setToken(result.access_token);
      setRefreshToken(result.refresh_token);
      this.user = await api.me();
    },
    async register(username: string, email: string, password: string) {
      await api.register(username, email, password);
      await this.login(email, password);
    },
    async logout() {
      const refreshToken = getRefreshToken();
      if (refreshToken) {
        try {
          await api.logout(refreshToken);
        } catch {
          // 服务端不可用时也要允许本地退出
        }
      }
      this.token = null;
      this.user = null;
      setToken(null);
      setRefreshToken(null);
    },
  },
});

export const useToastStore = defineStore("toast", {
  state: () => ({
    items: [] as ToastItem[],
    seq: 0,
  }),
  actions: {
    push(text: string, kind: "info" | "error" = "info") {
      const id = ++this.seq;
      this.items.push({ id, text, kind });
      window.setTimeout(() => this.dismiss(id), 2600);
    },
    error(error: unknown) {
      const text = error instanceof Error ? error.message : "操作失败";
      this.push(text, "error");
    },
    dismiss(id: number) {
      this.items = this.items.filter((item) => item.id !== id);
    },
  },
});

export const useNotificationStore = defineStore("notifications", {
  state: () => ({
    unreadCount: 0,
  }),
  actions: {
    async refreshUnread() {
      if (!getToken()) {
        this.unreadCount = 0;
        return;
      }
      try {
        const result = await api.notificationUnreadCount();
        this.unreadCount = result.count;
      } catch {
        // 通知计数失败不能影响主页面加载，SSE 或下次进入页面会重新同步。
      }
    },
    increment() {
      this.unreadCount += 1;
    },
    clear() {
      this.unreadCount = 0;
    },
  },
});

export const useMessageStore = defineStore("messages", {
  state: () => ({
    unreadCount: 0,
    latestEvent: null as MessageItem | null,
  }),
  actions: {
    async refreshUnread() {
      if (!getToken()) {
        this.unreadCount = 0;
        return;
      }
      try {
        const result = await api.messageUnreadCount();
        this.unreadCount = result.count;
      } catch {
        // 私信计数失败不影响主页面；进入消息页时会再次同步。
      }
    },
    receive(item: MessageItem) {
      this.unreadCount += 1;
      this.latestEvent = item;
    },
    clear() {
      this.unreadCount = 0;
      this.latestEvent = null;
    },
  },
});
