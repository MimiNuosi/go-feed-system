<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Flame, RefreshCw } from "lucide-vue-next";
import { api, type FeedItem } from "../api";
import { useToastStore } from "../stores";

const toast = useToastStore();
const items = ref<FeedItem[]>([]);
const windowName = ref<"1h" | "24h">("1h");
const loading = ref(false);

async function load() {
  loading.value = true;
  try {
    const page = await api.hot(windowName.value);
    items.value = page.items;
  } catch (error) {
    toast.error(error);
  } finally {
    loading.value = false;
  }
}

function choose(name: "1h" | "24h") {
  if (windowName.value === name) {
    return;
  }
  windowName.value = name;
  void load();
}

onMounted(load);
</script>

<template>
  <section class="page">
    <header class="head">
      <div>
        <h1 class="page-title"><Flame :size="20" /> 热榜</h1>
        <p class="muted">按播放、点赞、评论加权，滑动窗口实时累计</p>
      </div>
      <div class="actions">
        <div class="seg">
          <button :class="{ on: windowName === '1h' }" @click="choose('1h')">近 1 小时</button>
          <button :class="{ on: windowName === '24h' }" @click="choose('24h')">近 24 小时</button>
        </div>
        <button class="btn" :disabled="loading" @click="load">
          <RefreshCw :size="15" :class="{ spin: loading }" /> 刷新
        </button>
      </div>
    </header>

    <p v-if="!items.length && !loading" class="empty">这个窗口还没有数据</p>

    <ol class="rank-list">
      <li v-for="(item, index) in items" :key="item.id" class="rank-row">
        <span class="rank" :class="{ top: index < 3 }">{{ index + 1 }}</span>
        <RouterLink :to="`/video/${item.id}`" class="row-main">
          <div class="row-title">{{ item.title }}</div>
          <div class="row-meta">
            <span>@{{ item.author.username }}</span>
            <span>{{ item.view_count }} 播放</span>
            <span>{{ item.like_count }} 点赞</span>
          </div>
        </RouterLink>
        <RouterLink :to="`/video/${item.id}`" class="btn small">观看</RouterLink>
      </li>
    </ol>
  </section>
</template>

<style scoped>
.page {
  padding: 28px 32px 40px;
  max-width: 980px;
  margin: 0 auto;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 18px;
}

.page-title {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 4px;
}

.actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.seg {
  display: flex;
  border: 1px solid var(--line);
  border-radius: 8px;
  overflow: hidden;
}

.seg button {
  padding: 8px 12px;
  font-size: 13px;
  color: var(--muted);
}

.seg button.on {
  background: var(--accent-soft);
  color: var(--text);
}

.rank-list {
  list-style: none;
  margin: 0;
  padding: 0;
  border-top: 1px solid var(--line);
}

.rank-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 16px 4px;
  border-bottom: 1px solid var(--line);
}

.rank {
  width: 28px;
  text-align: center;
  font-size: 18px;
  font-weight: 700;
  color: var(--muted);
}

.rank.top {
  color: var(--accent);
}

.row-main {
  flex: 1;
  min-width: 0;
}

.row-title {
  font-size: 15px;
  font-weight: 600;
  margin-bottom: 5px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-meta {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--muted);
}

.btn.small {
  padding: 6px 12px;
  font-size: 13px;
}

.empty {
  color: var(--muted);
  text-align: center;
  padding: 60px 0;
}

.spin {
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 820px) {
  .page {
    padding: 18px 16px 80px;
  }

  .head {
    flex-direction: column;
  }
}
</style>
