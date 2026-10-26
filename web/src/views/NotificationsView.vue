<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Bell, Heart, MessageCircle, UserPlus } from "lucide-vue-next";
import { api, type NotificationItem, type NotificationKind } from "../api";
import { useNotificationStore, useToastStore } from "../stores";

const toast = useToastStore();
const notificationStore = useNotificationStore();

const items = ref<NotificationItem[]>([]);
const nextCursor = ref<string | undefined>();
const hasMore = ref(false);
const loading = ref(false);
const loadingMore = ref(false);

const kindMeta: Record<
  NotificationKind,
  { icon: typeof Heart; label: string }
> = {
  like: { icon: Heart, label: "赞了你的作品" },
  comment: { icon: MessageCircle, label: "评论了你的作品" },
  follow: { icon: UserPlus, label: "关注了你" },
};

const unreadLabel = computed(() =>
  notificationStore.unreadCount > 0
    ? `${notificationStore.unreadCount} 条未读`
    : "已全部读完",
);

function meta(item: NotificationItem) {
  return kindMeta[item.kind] ?? { icon: Bell, label: "与你互动" };
}

function itemTarget(item: NotificationItem): string {
  if (item.video_id) {
    return `/video/${item.video_id}`;
  }
  return `/u/${item.actor.id}`;
}

function formatTime(value: string): string {
  const date = new Date(value);
  const seconds = Math.max(0, (Date.now() - date.getTime()) / 1000);
  if (seconds < 60) {
    return "刚刚";
  }
  if (seconds < 3600) {
    return `${Math.floor(seconds / 60)} 分钟前`;
  }
  if (seconds < 86400) {
    return `${Math.floor(seconds / 3600)} 小时前`;
  }
  if (seconds < 86400 * 7) {
    return `${Math.floor(seconds / 86400)} 天前`;
  }
  return date.toLocaleDateString("zh-CN");
}

async function markAllRead() {
  try {
    await api.markAllNotificationsRead();
    items.value = items.value.map((item) => ({ ...item, read: true }));
    notificationStore.clear();
  } catch (error) {
    toast.error(error);
  }
}

async function load() {
  loading.value = true;
  try {
    const page = await api.notifications();
    items.value = page.items;
    nextCursor.value = page.next_cursor;
    hasMore.value = page.has_more;
    if (page.items.some((item) => !item.read)) {
      await markAllRead();
    }
  } catch (error) {
    toast.error(error);
  } finally {
    loading.value = false;
  }
}

async function loadMore() {
  if (loadingMore.value || !hasMore.value) {
    return;
  }
  loadingMore.value = true;
  try {
    const page = await api.notifications(nextCursor.value);
    items.value.push(...page.items);
    nextCursor.value = page.next_cursor;
    hasMore.value = page.has_more;
  } catch (error) {
    toast.error(error);
  } finally {
    loadingMore.value = false;
  }
}

onMounted(load);
</script>

<template>
  <section class="page">
    <header class="head">
      <div>
        <h1 class="page-title">通知</h1>
        <p class="muted">{{ unreadLabel }}</p>
      </div>
      <button
        v-if="notificationStore.unreadCount > 0"
        class="btn"
        @click="markAllRead"
      >
        全部已读
      </button>
    </header>

    <p v-if="loading" class="muted state">加载中…</p>
    <div v-else-if="!items.length" class="empty">
      <Bell :size="30" />
      <p>还没有通知</p>
      <span class="muted">有人点赞、评论或关注你时，会显示在这里。</span>
    </div>

    <div v-else class="list">
      <RouterLink
        v-for="item in items"
        :key="item.id"
        :to="itemTarget(item)"
        class="row"
        :class="{ unread: !item.read }"
      >
        <img
          v-if="item.actor.avatar_url"
          :src="item.actor.avatar_url"
          alt=""
          class="avatar"
        />
        <span v-else class="avatar fallback">
          {{ item.actor.username.slice(0, 1).toUpperCase() }}
        </span>
        <span class="body">
          <span class="line">
            <b>{{ item.actor.username }}</b>
            {{ meta(item).label }}
          </span>
          <span v-if="item.kind === 'comment' && item.content" class="quote">
            “{{ item.content }}”
          </span>
          <span class="time">{{ formatTime(item.created_at) }}</span>
        </span>
        <span class="kind">
          <component :is="meta(item).icon" :size="18" />
        </span>
      </RouterLink>

      <button v-if="hasMore" class="more btn" :disabled="loadingMore" @click="loadMore">
        {{ loadingMore ? "加载中…" : "加载更多" }}
      </button>
    </div>
  </section>
</template>

<style scoped>
.page {
  max-width: 820px;
  margin: 0 auto;
  padding: 30px 24px 70px;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.list {
  border-top: 1px solid var(--line);
}

.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 15px 8px;
  border-bottom: 1px solid var(--line);
  transition: background 0.15s ease;
}

.row:hover {
  background: var(--panel);
}

.row.unread {
  background: rgba(254, 44, 85, 0.06);
}

.avatar {
  width: 42px;
  height: 42px;
  flex: 0 0 42px;
  border-radius: 50%;
  object-fit: cover;
}

.avatar.fallback {
  display: grid;
  place-items: center;
  background: #2a2a34;
  font-weight: 700;
}

.body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.line {
  font-size: 14px;
}

.line b {
  margin-right: 4px;
}

.quote {
  overflow: hidden;
  color: var(--muted);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.time {
  color: var(--muted);
  font-size: 12px;
}

.kind {
  width: 34px;
  height: 34px;
  flex: 0 0 34px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: var(--panel-2);
  color: var(--muted);
}

.more {
  width: 100%;
  margin-top: 18px;
}

.state,
.empty {
  padding: 70px 0;
  text-align: center;
}

.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  color: var(--muted);
}

.empty p {
  margin: 4px 0 0;
  color: var(--text);
  font-weight: 600;
}

@media (max-width: 820px) {
  .page {
    padding: 18px 16px 90px;
  }
}
</style>
