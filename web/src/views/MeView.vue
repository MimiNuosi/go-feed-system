<script setup lang="ts">
import { onMounted, ref } from "vue";
import { PlusSquare } from "lucide-vue-next";
import { api, type FeedItem } from "../api";
import { useAuthStore, useToastStore } from "../stores";

const auth = useAuthStore();
const toast = useToastStore();
const items = ref<FeedItem[]>([]);
const loading = ref(false);

async function load() {
  if (!auth.user) {
    return;
  }
  loading.value = true;
  try {
    const page = await api.myVideos(auth.user.id);
    items.value = page.items;
  } catch (error) {
    toast.error(error);
  } finally {
    loading.value = false;
  }
}

onMounted(load);
</script>

<template>
  <section class="page">
    <header class="head">
      <div>
        <h1 class="page-title">{{ auth.user?.username ?? "我的作品" }}</h1>
        <p class="muted">{{ items.length }} 个作品</p>
      </div>
      <RouterLink to="/upload" class="btn primary"><PlusSquare :size="16" /> 发布新作品</RouterLink>
    </header>

    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="!items.length" class="empty muted">还没有作品，发布第一个吧</p>

    <div class="grid">
      <RouterLink v-for="item in items" :key="item.id" :to="`/video/${item.id}`" class="tile">
        <div class="thumb">▶</div>
        <div class="tile-title">{{ item.title }}</div>
        <div class="tile-meta">{{ item.view_count }} 播放 · {{ item.like_count }} 点赞</div>
      </RouterLink>
    </div>
  </section>
</template>

<style scoped>
.page {
  max-width: 1000px;
  margin: 0 auto;
  padding: 28px 24px 60px;
}

.head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(190px, 1fr));
  gap: 14px;
}

.tile {
  display: flex;
  flex-direction: column;
  gap: 6px;
  padding: 10px;
  border: 1px solid var(--line);
  border-radius: var(--radius);
  background: var(--panel);
}

.tile:hover {
  border-color: #3a3a46;
}

.thumb {
  aspect-ratio: 9 / 14;
  border-radius: 6px;
  background: #000;
  display: grid;
  place-items: center;
  color: #4a4a56;
  font-size: 26px;
}

.tile-title {
  font-size: 14px;
  font-weight: 600;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tile-meta {
  font-size: 12px;
  color: var(--muted);
}

.empty {
  padding: 60px 0;
  text-align: center;
}

@media (max-width: 820px) {
  .page {
    padding: 18px 16px 80px;
  }
}
</style>
