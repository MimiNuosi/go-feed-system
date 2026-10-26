<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from "vue";
import { ArrowLeft, MessageCircle, Send } from "lucide-vue-next";
import { useRoute, useRouter } from "vue-router";
import {
  api,
  type Conversation,
  type MessageItem,
  type Profile,
} from "../api";
import { useAuthStore, useMessageStore, useToastStore } from "../stores";

const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const messageStore = useMessageStore();
const toast = useToastStore();

const conversations = ref<Conversation[]>([]);
const directProfile = ref<Profile | null>(null);
const items = ref<MessageItem[]>([]);
const nextCursor = ref<string | undefined>();
const hasMore = ref(false);
const loadingConversations = ref(false);
const loadingMessages = ref(false);
const loadingOlder = ref(false);
const sending = ref(false);
const draft = ref("");
const scroller = ref<HTMLElement | null>(null);

const peerId = computed(() => Number(route.params.peerId) || 0);
const activeConversation = computed(() =>
  conversations.value.find((item) => item.peer.id === peerId.value),
);
const activePeer = computed(() => {
  if (activeConversation.value) {
    return activeConversation.value.peer;
  }
  if (!directProfile.value) {
    return null;
  }
  return {
    ...directProfile.value.user,
    avatar_url: directProfile.value.avatar_url,
  };
});

function formatTime(value: string): string {
  const date = new Date(value);
  const now = new Date();
  if (date.toDateString() === now.toDateString()) {
    return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
  }
  return date.toLocaleDateString("zh-CN", { month: "numeric", day: "numeric" });
}

async function scrollToBottom() {
  await nextTick();
  if (scroller.value) {
    scroller.value.scrollTop = scroller.value.scrollHeight;
  }
}

async function loadConversations() {
  loadingConversations.value = true;
  try {
    conversations.value = (await api.conversations()).items;
  } catch (error) {
    toast.error(error);
  } finally {
    loadingConversations.value = false;
  }
}

async function loadPeer() {
  directProfile.value = null;
  if (!peerId.value) {
    return;
  }
  if (activePeer.value) {
    return;
  }
  try {
    directProfile.value = await api.profile(peerId.value);
  } catch (error) {
    toast.error(error);
  }
}

async function markRead() {
  if (!peerId.value) {
    return;
  }
  try {
    await api.markConversationRead(peerId.value);
    items.value = items.value.map((item) => ({ ...item, read: true }));
    const conversation = activeConversation.value;
    if (conversation) {
      conversation.unread_count = 0;
    }
    await messageStore.refreshUnread();
  } catch (error) {
    toast.error(error);
  }
}

async function loadMessages() {
  if (!peerId.value) {
    items.value = [];
    nextCursor.value = undefined;
    hasMore.value = false;
    return;
  }
  loadingMessages.value = true;
  try {
    const page = await api.messages(peerId.value);
    items.value = page.items;
    nextCursor.value = page.next_cursor;
    hasMore.value = page.has_more;
    await markRead();
    await scrollToBottom();
  } catch (error) {
    toast.error(error);
    items.value = [];
  } finally {
    loadingMessages.value = false;
  }
}

async function loadOlder() {
  if (!peerId.value || !hasMore.value || loadingOlder.value) {
    return;
  }
  const previousHeight = scroller.value?.scrollHeight ?? 0;
  loadingOlder.value = true;
  try {
    const page = await api.messages(peerId.value, nextCursor.value);
    items.value = [...page.items, ...items.value];
    nextCursor.value = page.next_cursor;
    hasMore.value = page.has_more;
    await nextTick();
    if (scroller.value) {
      scroller.value.scrollTop = scroller.value.scrollHeight - previousHeight;
    }
  } catch (error) {
    toast.error(error);
  } finally {
    loadingOlder.value = false;
  }
}

async function send() {
  const content = draft.value.trim();
  if (!peerId.value || !content || sending.value) {
    return;
  }
  sending.value = true;
  try {
    const created = await api.sendMessage(peerId.value, content);
    items.value.push(created);
    draft.value = "";
    await scrollToBottom();
    await loadConversations();
  } catch (error) {
    toast.error(error);
  } finally {
    sending.value = false;
  }
}

async function syncRoute() {
  await loadConversations();
  await loadPeer();
  await loadMessages();
}

watch(
  () => route.params.peerId,
  () => {
    void syncRoute();
  },
);

watch(
  () => messageStore.latestEvent,
  async (event) => {
    if (!event) {
      return;
    }
    await loadConversations();
    if (event.sender_id !== peerId.value) {
      return;
    }
    if (!items.value.some((item) => item.id === event.id)) {
      items.value.push(event);
      await scrollToBottom();
    }
    await markRead();
  },
);

onMounted(syncRoute);
</script>

<template>
  <section class="page" :class="{ chatting: peerId > 0 }">
    <aside class="conversation-pane">
      <header>
        <h1 class="page-title">私信</h1>
        <span class="muted">{{ conversations.length }} 个会话</span>
      </header>
      <p v-if="loadingConversations" class="muted state">加载中…</p>
      <div v-else-if="!conversations.length" class="empty muted">
        <MessageCircle :size="26" />
        <span>还没有私信</span>
      </div>
      <div v-else class="conversation-list">
        <RouterLink
          v-for="item in conversations"
          :key="item.peer.id"
          :to="`/messages/${item.peer.id}`"
          class="conversation"
          :class="{ active: item.peer.id === peerId }"
        >
          <img
            v-if="item.peer.avatar_url"
            :src="item.peer.avatar_url"
            alt=""
            class="avatar"
          />
          <span v-else class="avatar fallback">
            {{ item.peer.username.slice(0, 1).toUpperCase() }}
          </span>
          <span class="conversation-body">
            <span class="conversation-head">
              <b>{{ item.peer.username }}</b>
              <span>{{ formatTime(item.last_message.created_at) }}</span>
            </span>
            <span class="preview">{{ item.last_message.content }}</span>
          </span>
          <span v-if="item.unread_count" class="unread">{{ item.unread_count }}</span>
        </RouterLink>
      </div>
    </aside>

    <div v-if="!peerId" class="placeholder">
      <MessageCircle :size="34" />
      <p>选择一个会话开始聊天</p>
      <span class="muted">也可以从用户主页点击“私信”。</span>
    </div>

    <main v-else class="chat">
      <header class="chat-head">
        <button class="back" @click="router.push('/messages')">
          <ArrowLeft :size="18" />
        </button>
        <img
          v-if="activePeer?.avatar_url"
          :src="activePeer.avatar_url"
          alt=""
          class="avatar"
        />
        <span v-else class="avatar fallback">
          {{ activePeer?.username?.slice(0, 1).toUpperCase() ?? "?" }}
        </span>
        <div>
          <strong>{{ activePeer?.username ?? `用户 #${peerId}` }}</strong>
          <RouterLink :to="`/u/${peerId}`" class="profile-link">查看主页</RouterLink>
        </div>
      </header>

      <section ref="scroller" class="message-list">
        <button
          v-if="hasMore"
          class="load-older btn"
          :disabled="loadingOlder"
          @click="loadOlder"
        >
          {{ loadingOlder ? "加载中…" : "加载更早消息" }}
        </button>
        <p v-if="loadingMessages" class="muted state">加载中…</p>
        <p v-else-if="!items.length" class="muted state">还没有消息，打个招呼吧</p>
        <div
          v-for="item in items"
          :key="item.id"
          class="message-row"
          :class="{ mine: item.sender_id === auth.user?.id }"
        >
          <div class="bubble">
            <p>{{ item.content }}</p>
            <time>{{ formatTime(item.created_at) }}</time>
          </div>
        </div>
      </section>

      <form class="composer" @submit.prevent="send">
        <input
          v-model="draft"
          maxlength="2000"
          autocomplete="off"
          placeholder="输入消息"
        />
        <button class="btn primary" :disabled="sending || !draft.trim()">
          <Send :size="17" />
          {{ sending ? "发送中" : "发送" }}
        </button>
      </form>
    </main>
  </section>
</template>

<style scoped>
.page {
  display: grid;
  grid-template-columns: 320px minmax(0, 1fr);
  height: 100%;
  background: var(--bg);
}

.conversation-pane {
  min-width: 0;
  border-right: 1px solid var(--line);
  background: var(--panel);
  overflow-y: auto;
}

.conversation-pane header {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  padding: 24px 18px 14px;
}

.conversation-pane .page-title {
  margin: 0;
}

.conversation-list {
  padding: 0 8px;
}

.conversation {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 10px;
  border-radius: 8px;
}

.conversation:hover,
.conversation.active {
  background: var(--panel-2);
}

.avatar {
  width: 40px;
  height: 40px;
  flex: 0 0 40px;
  border-radius: 50%;
  object-fit: cover;
}

.avatar.fallback {
  display: grid;
  place-items: center;
  background: #2a2a34;
  font-weight: 700;
}

.conversation-body {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.conversation-head {
  display: flex;
  justify-content: space-between;
  gap: 8px;
}

.conversation-head span {
  color: var(--muted);
  font-size: 11px;
}

.preview {
  overflow: hidden;
  color: var(--muted);
  font-size: 12px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.unread {
  min-width: 19px;
  height: 19px;
  padding: 0 5px;
  border-radius: 999px;
  display: grid;
  place-items: center;
  background: var(--accent);
  color: #fff;
  font-size: 11px;
  font-weight: 700;
}

.placeholder,
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: var(--muted);
  text-align: center;
}

.placeholder p {
  margin: 6px 0 0;
  color: var(--text);
  font-weight: 600;
}

.empty {
  min-height: 180px;
}

.state {
  padding: 28px 16px;
  text-align: center;
}

.chat {
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.chat-head {
  height: 68px;
  flex: 0 0 68px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 20px;
  border-bottom: 1px solid var(--line);
  background: var(--panel);
}

.chat-head > div {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.profile-link {
  color: var(--muted);
  font-size: 12px;
}

.back {
  display: none;
}

.message-list {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.load-older {
  align-self: center;
  font-size: 12px;
}

.message-row {
  display: flex;
  justify-content: flex-start;
}

.message-row.mine {
  justify-content: flex-end;
}

.bubble {
  max-width: min(72%, 520px);
  padding: 9px 12px 7px;
  border-radius: 10px;
  background: var(--panel-2);
}

.mine .bubble {
  background: var(--accent);
  color: #fff;
}

.bubble p {
  margin: 0 0 5px;
  line-height: 1.55;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.bubble time {
  display: block;
  color: var(--muted);
  font-size: 10px;
  text-align: right;
}

.mine .bubble time {
  color: rgba(255, 255, 255, 0.7);
}

.composer {
  flex: 0 0 auto;
  display: flex;
  gap: 10px;
  padding: 14px 18px;
  border-top: 1px solid var(--line);
  background: var(--panel);
}

.composer input {
  flex: 1;
  min-width: 0;
}

@media (max-width: 820px) {
  .page {
    display: block;
    padding-bottom: 56px;
  }

  .conversation-pane {
    height: 100%;
    border-right: none;
  }

  .page.chatting .conversation-pane {
    display: none;
  }

  .chat {
    height: 100%;
  }

  .placeholder {
    display: none;
  }

  .chat-head {
    height: 60px;
    flex-basis: 60px;
    padding: 0 12px;
  }

  .back {
    width: 34px;
    height: 34px;
    display: grid;
    place-items: center;
  }

  .message-list {
    padding: 14px 12px;
  }

  .bubble {
    max-width: 82%;
  }

  .composer {
    padding: 10px 12px;
  }
}
</style>
