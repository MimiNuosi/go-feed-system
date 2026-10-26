<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Send, Trash2, X } from "lucide-vue-next";
import { api, type CommentItem } from "../api";
import { useAuthStore, useToastStore } from "../stores";

const props = defineProps<{ videoId: number }>();
const emit = defineEmits<{ (event: "close"): void }>();

const auth = useAuthStore();
const toast = useToastStore();
const comments = ref<CommentItem[]>([]);
const content = ref("");
const loading = ref(false);
const sending = ref(false);

async function load() {
  loading.value = true;
  try {
    const page = await api.comments(props.videoId);
    comments.value = page.items;
  } catch (error) {
    toast.error(error);
  } finally {
    loading.value = false;
  }
}

async function submit() {
  const text = content.value.trim();
  if (!text) {
    return;
  }
  if (!auth.isLoggedIn) {
    toast.push("登录后才能评论");
    return;
  }
  sending.value = true;
  try {
    const created = await api.addComment(props.videoId, text);
    comments.value = [
      { ...created, username: auth.user?.username ?? created.username },
      ...comments.value,
    ];
    content.value = "";
  } catch (error) {
    toast.error(error);
  } finally {
    sending.value = false;
  }
}

async function remove(id: number) {
  try {
    await api.deleteComment(id);
    comments.value = comments.value.filter((item) => item.id !== id);
  } catch (error) {
    toast.error(error);
  }
}

onMounted(load);
</script>

<template>
  <aside class="comments">
    <header class="head">
      <span>评论 {{ comments.length }}</span>
      <button aria-label="关闭评论" @click="emit('close')"><X :size="18" /></button>
    </header>

    <div class="list">
      <p v-if="loading" class="empty">加载中…</p>
      <p v-else-if="!comments.length" class="empty">还没有评论，来说两句</p>
      <article v-for="item in comments" :key="item.id" class="comment">
        <span class="avatar">{{ item.username.slice(0, 1).toUpperCase() }}</span>
        <div class="body">
          <div class="who">{{ item.username }}</div>
          <p class="text">{{ item.content }}</p>
        </div>
        <button
          v-if="auth.user?.id === item.user_id"
          class="del"
          aria-label="删除评论"
          @click="remove(item.id)"
        >
          <Trash2 :size="15" />
        </button>
      </article>
    </div>

    <form class="compose" @submit.prevent="submit">
      <input v-model="content" placeholder="说点什么…" maxlength="1000" />
      <button class="btn primary" type="submit" :disabled="sending || !content.trim()">
        <Send :size="16" />
      </button>
    </form>
  </aside>
</template>

<style scoped>
.comments {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--panel);
  border-left: 1px solid var(--line);
}

.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px;
  border-bottom: 1px solid var(--line);
  font-weight: 600;
}

.list {
  flex: 1;
  overflow-y: auto;
  padding: 12px 16px;
}

.empty {
  color: var(--muted);
  font-size: 13px;
  text-align: center;
  margin-top: 32px;
}

.comment {
  display: flex;
  gap: 10px;
  padding: 10px 0;
  border-bottom: 1px solid #202028;
}

.avatar {
  width: 28px;
  height: 28px;
  flex: 0 0 28px;
  border-radius: 50%;
  background: #2a2a34;
  display: grid;
  place-items: center;
  font-size: 13px;
}

.body {
  flex: 1;
  min-width: 0;
}

.who {
  font-size: 13px;
  color: var(--muted);
  margin-bottom: 3px;
}

.text {
  margin: 0;
  font-size: 14px;
  line-height: 1.5;
  word-break: break-word;
}

.del {
  color: var(--muted);
  align-self: flex-start;
}

.del:hover {
  color: var(--accent);
}

.compose {
  display: flex;
  gap: 8px;
  padding: 12px;
  border-top: 1px solid var(--line);
}

.compose input {
  flex: 1;
}
</style>
