import { defineStore } from "pinia";
import { api, getToken, setToken, type User } from "./api";

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
      this.user = await api.me();
    },
    async register(username: string, email: string, password: string) {
      await api.register(username, email, password);
      await this.login(email, password);
    },
    logout() {
      this.token = null;
      this.user = null;
      setToken(null);
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
