<script setup lang="ts">
import { computed, onMounted, onUnmounted, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import {
  Bell,
  Flame,
  Home,
  LogIn,
  LogOut,
  MessagesSquare,
  PlusSquare,
  Settings,
  User,
  Users,
} from "lucide-vue-next";
import {
  streamMessages,
  streamNotifications,
  type MessageItem,
  type NotificationItem,
} from "./api";
import {
  useAuthStore,
  useMessageStore,
  useNotificationStore,
  useToastStore,
} from "./stores";

const auth = useAuthStore();
const toast = useToastStore();
const notifications = useNotificationStore();
const messages = useMessageStore();
const route = useRoute();
const router = useRouter();

const navItems = [
  { to: "/", label: "首页", icon: Home },
  { to: "/following", label: "关注", icon: Users },
  { to: "/hot", label: "热榜", icon: Flame },
  { to: "/notifications", label: "通知", icon: Bell },
  { to: "/messages", label: "私信", icon: MessagesSquare },
  { to: "/upload", label: "发布", icon: PlusSquare },
  { to: "/me", label: "我的", icon: User },
];

const userInitial = computed(() => auth.user?.username.slice(0, 1).toUpperCase() ?? "?");
const isFeed = computed(() => route.name === "latest" || route.name === "following");
let notificationAbort: AbortController | null = null;
let messageAbort: AbortController | null = null;

function notificationText(item: NotificationItem): string {
  if (item.kind === "like") {
    return `${item.actor.username} 赞了你的作品`;
  }
  if (item.kind === "comment") {
    return `${item.actor.username} 评论了你的作品`;
  }
  return `${item.actor.username} 关注了你`;
}

async function connectNotificationStream(signal: AbortSignal) {
  while (!signal.aborted) {
    try {
      await streamNotifications((item) => {
        notifications.increment();
        toast.push(notificationText(item));
      }, signal);
    } catch (error) {
      if (signal.aborted) {
        return;
      }
      console.warn("notification stream disconnected", error);
    }
    await new Promise((resolve) => window.setTimeout(resolve, 3000));
  }
}

function startNotificationStream() {
  notificationAbort?.abort();
  notificationAbort = null;
  if (!auth.isLoggedIn) {
    notifications.clear();
    return;
  }

  void notifications.refreshUnread();
  const controller = new AbortController();
  notificationAbort = controller;
  void connectNotificationStream(controller.signal);
}

function stopNotificationStream() {
  notificationAbort?.abort();
  notificationAbort = null;
}

async function connectMessageStream(signal: AbortSignal) {
  while (!signal.aborted) {
    try {
      await streamMessages((item: MessageItem) => {
        messages.receive(item);
        toast.push("收到一条新私信");
      }, signal);
    } catch (error) {
      if (signal.aborted) {
        return;
      }
      console.warn("message stream disconnected", error);
    }
    await new Promise((resolve) => window.setTimeout(resolve, 3000));
  }
}

function startMessageStream() {
  messageAbort?.abort();
  messageAbort = null;
  if (!auth.isLoggedIn) {
    messages.clear();
    return;
  }
  void messages.refreshUnread();
  const controller = new AbortController();
  messageAbort = controller;
  void connectMessageStream(controller.signal);
}

function stopMessageStream() {
  messageAbort?.abort();
  messageAbort = null;
}

async function logout() {
  await auth.logout();
  toast.push("已退出登录");
  router.push("/");
}

function onExpired() {
  void auth.logout();
  toast.push("登录已过期，请重新登录", "error");
  if (route.meta.requiresAuth) {
    router.push({ name: "login", query: { redirect: route.fullPath } });
  }
}

onMounted(() => {
  void auth.restore();
  window.addEventListener("auth:expired", onExpired);
});

onUnmounted(() => {
  stopNotificationStream();
  stopMessageStream();
  window.removeEventListener("auth:expired", onExpired);
});

watch(
  () => auth.isLoggedIn,
  () => {
    startNotificationStream();
    startMessageStream();
  },
  { immediate: true },
);
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <RouterLink to="/" class="brand">Feed</RouterLink>
      <nav class="nav">
        <RouterLink v-for="item in navItems" :key="item.to" :to="item.to" class="nav-link">
          <component :is="item.icon" :size="20" :stroke-width="2" />
          <span>{{ item.label }}</span>
          <span
            v-if="item.to === '/notifications' && notifications.unreadCount > 0"
            class="badge"
          >
            {{ notifications.unreadCount > 99 ? "99+" : notifications.unreadCount }}
          </span>
          <span
            v-else-if="item.to === '/messages' && messages.unreadCount > 0"
            class="badge"
          >
            {{ messages.unreadCount > 99 ? "99+" : messages.unreadCount }}
          </span>
        </RouterLink>
      </nav>
      <div class="sidebar-foot">
        <template v-if="auth.user">
          <div class="me">
            <img
              v-if="auth.user.avatar_url"
              :src="auth.user.avatar_url"
              :alt="`${auth.user.username} 的头像`"
              class="avatar"
            />
            <span v-else class="avatar fallback">{{ userInitial }}</span>
            <span class="name">{{ auth.user.username }}</span>
          </div>
          <RouterLink to="/settings" class="btn ghost">
            <Settings :size="16" /> 设置
          </RouterLink>
          <button class="btn ghost" @click="logout">
            <LogOut :size="16" /> 退出
          </button>
        </template>
        <RouterLink v-else to="/login" class="btn primary">
          <LogIn :size="16" /> 登录
        </RouterLink>
      </div>
    </aside>

    <main class="main" :class="{ feed: isFeed }">
      <RouterView />
    </main>

    <nav class="bottom-nav">
      <RouterLink v-for="item in navItems" :key="item.to" :to="item.to" class="bottom-link">
        <component :is="item.icon" :size="22" :stroke-width="2" />
        <span>{{ item.label }}</span>
        <span
          v-if="item.to === '/notifications' && notifications.unreadCount > 0"
          class="mobile-badge"
        >
          {{ notifications.unreadCount > 99 ? "99+" : notifications.unreadCount }}
        </span>
        <span
          v-else-if="item.to === '/messages' && messages.unreadCount > 0"
          class="mobile-badge"
        >
          {{ messages.unreadCount > 99 ? "99+" : messages.unreadCount }}
        </span>
      </RouterLink>
    </nav>

    <div class="toast-host">
      <div v-for="item in toast.items" :key="item.id" class="toast" :class="item.kind">
        {{ item.text }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  height: 100%;
}

.sidebar {
  width: 220px;
  flex: 0 0 220px;
  border-right: 1px solid var(--line);
  padding: 22px 16px;
  display: flex;
  flex-direction: column;
  gap: 24px;
  background: #0d0d12;
}

.brand {
  font-size: 26px;
  font-weight: 800;
  letter-spacing: 0.5px;
  color: var(--accent);
  padding-left: 8px;
}

.nav {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.nav-link {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 11px 12px;
  border-radius: 8px;
  color: var(--muted);
  font-size: 15px;
}

.nav-link:hover {
  background: var(--panel);
  color: var(--text);
}

.nav-link.router-link-active {
  background: var(--accent-soft);
  color: var(--text);
}

.badge {
  min-width: 19px;
  height: 19px;
  margin-left: auto;
  padding: 0 5px;
  border-radius: 999px;
  background: var(--accent);
  color: #fff;
  display: grid;
  place-items: center;
  font-size: 11px;
  font-weight: 700;
}

.sidebar-foot {
  margin-top: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.me {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 4px;
}

.avatar {
  width: 30px;
  height: 30px;
  flex: 0 0 30px;
  border-radius: 50%;
  object-fit: cover;
}

.avatar.fallback {
  background: var(--accent);
  color: #fff;
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 14px;
}

.name {
  font-size: 14px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.btn.ghost {
  justify-content: flex-start;
  color: var(--muted);
  background: transparent;
  border-color: transparent;
}

.btn.ghost:hover {
  background: var(--panel);
}

.main {
  flex: 1;
  min-width: 0;
  height: 100%;
  overflow-y: auto;
}

.main.feed {
  overflow: hidden;
}

.bottom-nav {
  display: none;
}

@media (max-width: 820px) {
  .sidebar {
    display: none;
  }

  .bottom-nav {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    height: 56px;
    display: flex;
    justify-content: space-around;
    align-items: center;
    background: rgba(13, 13, 18, 0.96);
    border-top: 1px solid var(--line);
    z-index: 20;
  }

  .bottom-link {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    font-size: 11px;
    color: var(--muted);
  }

  .bottom-link.router-link-active {
    color: var(--text);
  }

  .mobile-badge {
    position: absolute;
    top: -3px;
    right: -9px;
    min-width: 17px;
    height: 17px;
    padding: 0 4px;
    border-radius: 999px;
    background: var(--accent);
    color: #fff;
    display: grid;
    place-items: center;
    font-size: 10px;
    font-weight: 700;
  }

  .main {
    padding-bottom: 56px;
  }

  .main.feed {
    padding-bottom: 0;
  }
}
</style>
