<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { Flame, Home, Users } from "lucide-vue-next";
import { api, type FeedItem } from "../api";
import { useToastStore } from "../stores";
import VideoCard from "../components/VideoCard.vue";
import CommentPanel from "../components/CommentPanel.vue";

const props = defineProps<{ mode: "latest" | "following" }>();

const router = useRouter();
const toast = useToastStore();
const scroller = ref<HTMLElement | null>(null);
const items = ref<FeedItem[]>([]);
const cursor = ref<string | undefined>(undefined);
const hasMore = ref(true);
const loading = ref(false);
const activeIndex = ref(0);
const commentVideoId = ref<number | null>(null);

let observer: IntersectionObserver | null = null;

function fetchPage(reset: boolean) {
  if (loading.value) {
    return;
  }
  if (!reset && !hasMore.value) {
    return;
  }
  loading.value = true;
  const loader = props.mode === "following" ? api.following : api.latest;
  loader(reset ? undefined : cursor.value)
    .then((page) => {
      items.value = reset ? page.items : [...items.value, ...page.items];
      cursor.value = page.next_cursor;
      hasMore.value = page.has_more;
      if (reset) {
        activeIndex.value = 0;
        void nextTick(() => {
          scroller.value?.scrollTo({ top: 0 });
          observeSlides();
        });
      }
    })
    .catch((error) => {
      toast.error(error);
      hasMore.value = false;
    })
    .finally(() => {
      loading.value = false;
    });
}

function observeSlides() {
  observer?.disconnect();
  if (!scroller.value) {
    return;
  }
  observer = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (entry.isIntersecting && entry.intersectionRatio >= 0.6) {
          const index = Number((entry.target as HTMLElement).dataset.index ?? 0);
          activeIndex.value = index;
        }
      }
    },
    { root: scroller.value, threshold: [0.6] },
  );
  scroller.value.querySelectorAll<HTMLElement>(".slide").forEach((el) => observer?.observe(el));
}

function onScroll() {
  const el = scroller.value;
  if (!el || loading.value || !hasMore.value) {
    return;
  }
  if (items.value.length - activeIndex.value <= 2 && el.scrollTop + el.clientHeight >= el.scrollHeight - 200) {
    fetchPage(false);
  }
}

function onLikeUpdate(videoId: number, payload: { likeCount: number; liked: boolean }) {
  const item = items.value.find((candidate) => candidate.id === videoId);
  if (item) {
    item.like_count = payload.likeCount;
    item.is_liked_by = payload.liked;
  }
}

function openComments(videoId: number) {
  commentVideoId.value = videoId;
}

watch(
  () => props.mode,
  () => {
    items.value = [];
    cursor.value = undefined;
    hasMore.value = true;
    fetchPage(true);
  },
);

onMounted(() => {
  fetchPage(true);
});

onBeforeUnmount(() => {
  observer?.disconnect();
});
</script>

<template>
  <div class="feed-view">
    <header class="tabs">
      <RouterLink to="/" class="tab" :class="{ on: mode === 'latest' }">
        <Home :size="16" /> 首页
      </RouterLink>
      <RouterLink to="/following" class="tab" :class="{ on: mode === 'following' }">
        <Users :size="16" /> 关注
      </RouterLink>
      <RouterLink to="/hot" class="tab">
        <Flame :size="16" /> 热榜
      </RouterLink>
    </header>

    <div ref="scroller" class="scroller" @scroll.passive="onScroll">
      <div v-for="(item, index) in items" :key="item.id" class="slide" :data-index="index">
        <VideoCard
          :item="item"
          :active="index === activeIndex"
          @update="(payload) => onLikeUpdate(item.id, payload)"
          @open-comments="openComments"
        />
      </div>

      <div v-if="!items.length && !loading" class="empty">
        <p>{{ mode === "following" ? "还没有关注的人发布作品" : "暂时没有视频" }}</p>
        <button class="btn primary" @click="router.push('/hot')">去热榜看看</button>
      </div>

      <div v-if="items.length" class="foot">{{ loading ? "加载中…" : hasMore ? "上滑加载更多" : "没有更多了" }}</div>
    </div>

    <CommentPanel
      v-if="commentVideoId !== null"
      class="drawer"
      :video-id="commentVideoId"
      @close="commentVideoId = null"
    />
  </div>
</template>

<style scoped>
.feed-view {
  position: relative;
  height: 100%;
  background: #000;
}

.tabs {
  position: absolute;
  top: 0;
  left: 50%;
  transform: translateX(-50%);
  z-index: 10;
  display: flex;
  gap: 22px;
  padding: 14px 20px;
  font-size: 15px;
  color: rgba(255, 255, 255, 0.62);
}

.tab {
  display: flex;
  align-items: center;
  gap: 6px;
  text-shadow: 0 1px 6px rgba(0, 0, 0, 0.6);
}

.tab.on {
  color: #fff;
  font-weight: 600;
}

.scroller {
  height: 100%;
  overflow-y: auto;
  scroll-snap-type: y mandatory;
  overscroll-behavior: contain;
}

.slide {
  height: 100%;
  scroll-snap-align: start;
}

.empty {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  color: var(--muted);
}

.foot {
  padding: 16px;
  text-align: center;
  font-size: 12px;
  color: var(--muted);
}

.drawer {
  position: absolute;
  top: 0;
  right: 0;
  bottom: 0;
  width: min(380px, 100%);
  z-index: 15;
  box-shadow: -12px 0 30px rgba(0, 0, 0, 0.45);
}

@media (max-width: 820px) {
  .drawer {
    width: 100%;
  }
}
</style>
