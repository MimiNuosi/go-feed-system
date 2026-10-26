<script setup lang="ts">
import { computed, onMounted, onUnmounted } from "vue";
import { useRoute, useRouter } from "vue-router";
import { Flame, Home, LogIn, LogOut, PlusSquare, Settings, User, Users } from "lucide-vue-next";
import { useAuthStore, useToastStore } from "./stores";

const auth = useAuthStore();
const toast = useToastStore();
const route = useRoute();
const router = useRouter();

const navItems = [
  { to: "/", label: "首页", icon: Home },
  { to: "/following", label: "关注", icon: Users },
  { to: "/hot", label: "热榜", icon: Flame },
  { to: "/upload", label: "发布", icon: PlusSquare },
  { to: "/me", label: "我的", icon: User },
];

const userInitial = computed(() => auth.user?.username.slice(0, 1).toUpperCase() ?? "?");
const isFeed = computed(() => route.name === "latest" || route.name === "following");

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
  window.removeEventListener("auth:expired", onExpired);
});
</script>

<template>
  <div class="shell">
    <aside class="sidebar">
      <RouterLink to="/" class="brand">Feed</RouterLink>
      <nav class="nav">
        <RouterLink v-for="item in navItems" :key="item.to" :to="item.to" class="nav-link">
          <component :is="item.icon" :size="20" :stroke-width="2" />
          <span>{{ item.label }}</span>
        </RouterLink>
      </nav>
      <div class="sidebar-foot">
        <template v-if="auth.user">
          <div class="me">
            <span class="avatar">{{ userInitial }}</span>
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
  border-radius: 50%;
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

  .main {
    padding-bottom: 56px;
  }

  .main.feed {
    padding-bottom: 0;
  }
}
</style>
