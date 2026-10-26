<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { Heart, MessageCircle, Play, Volume2, VolumeX } from "lucide-vue-next";
import { api, videoFileURL, type FeedItem } from "../api";
import { useAuthStore, useToastStore } from "../stores";

const props = defineProps<{
  item: FeedItem;
  active: boolean;
}>();

const emit = defineEmits<{
  (event: "update", payload: { likeCount: number; liked: boolean }): void;
  (event: "open-comments", videoId: number): void;
}>();

const auth = useAuthStore();
const toast = useToastStore();
const router = useRouter();

const videoEl = ref<HTMLVideoElement | null>(null);
const muted = ref(true);
const playing = ref(false);
const liked = ref(props.item.is_liked_by);
const likeCount = ref(props.item.like_count);

let viewTimer: number | undefined;
let recordedView = false;

const authorInitial = computed(() => props.item.author.username.slice(0, 1).toUpperCase());

function clearViewTimer() {
  if (viewTimer !== undefined) {
    window.clearTimeout(viewTimer);
    viewTimer = undefined;
  }
}

function startViewTimer() {
  clearViewTimer();
  if (recordedView) {
    return;
  }
  viewTimer = window.setTimeout(() => {
    recordedView = true;
    void api.view(props.item.id).catch(() => undefined);
  }, 3000);
}

async function play() {
  const video = videoEl.value;
  if (!video) {
    return;
  }
  try {
    await video.play();
    playing.value = true;
    startViewTimer();
  } catch {
    playing.value = false;
  }
}

function pause() {
  videoEl.value?.pause();
  playing.value = false;
  clearViewTimer();
}

watch(
  () => props.active,
  (active) => {
    if (active) {
      void play();
    } else {
      pause();
    }
  },
  { immediate: true },
);

function togglePlay() {
  if (playing.value) {
    pause();
  } else {
    void play();
  }
}

function toggleMute() {
  muted.value = !muted.value;
}

async function toggleLike() {
  if (!auth.isLoggedIn) {
    toast.push("登录后才能点赞");
    void router.push({ name: "login", query: { redirect: router.currentRoute.value.fullPath } });
    return;
  }
  const nextLiked = !liked.value;
  try {
    await api.like(props.item.id, nextLiked);
    liked.value = nextLiked;
    likeCount.value = Math.max(0, likeCount.value + (nextLiked ? 1 : -1));
    emit("update", { likeCount: likeCount.value, liked: liked.value });
  } catch (error) {
    toast.error(error);
  }
}

function openComments() {
  emit("open-comments", props.item.id);
}

onUnmounted(clearViewTimer);
</script>

<template>
  <section class="video-card">
    <video
      ref="videoEl"
      class="video"
      :src="videoFileURL(item.id)"
      loop
      playsinline
      preload="metadata"
      :muted="muted"
      @click="togglePlay"
    />

    <button v-if="!playing" class="play-hint" aria-label="播放" @click="togglePlay">
      <Play :size="46" fill="currentColor" />
    </button>

    <div class="rail">
      <button class="rail-btn" :class="{ liked }" aria-label="点赞" @click="toggleLike">
        <span class="rail-icon"><Heart :size="26" :fill="liked ? 'currentColor' : 'none'" /></span>
        <span class="rail-count">{{ likeCount }}</span>
      </button>
      <button class="rail-btn" aria-label="评论" @click="openComments">
        <span class="rail-icon"><MessageCircle :size="26" /></span>
        <span class="rail-count">评论</span>
      </button>
      <button class="rail-btn" aria-label="静音切换" @click="toggleMute">
        <span class="rail-icon">
          <VolumeX v-if="muted" :size="24" />
          <Volume2 v-else :size="24" />
        </span>
      </button>
    </div>

    <div class="info">
      <div class="author">
        <span class="avatar">{{ authorInitial }}</span>
        <RouterLink :to="`/u/${item.author.id}`" class="username">@{{ item.author.username }}</RouterLink>
      </div>
      <h2 class="title">{{ item.title }}</h2>
      <p v-if="item.description" class="desc">{{ item.description }}</p>
      <div class="meta">
        <span>{{ item.view_count }} 播放</span>
        <span>{{ item.like_count }} 点赞</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.video-card {
  position: relative;
  height: 100%;
  background: #000;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  scroll-snap-align: start;
}

.video {
  height: 100%;
  max-width: 100%;
  object-fit: contain;
  background: #000;
  cursor: pointer;
}

.play-hint {
  position: absolute;
  inset: 0;
  margin: auto;
  width: 74px;
  height: 74px;
  display: grid;
  place-items: center;
  border-radius: 50%;
  color: rgba(255, 255, 255, 0.9);
  background: rgba(0, 0, 0, 0.35);
  backdrop-filter: blur(2px);
}

.rail {
  position: absolute;
  right: 18px;
  bottom: 120px;
  display: flex;
  flex-direction: column;
  gap: 18px;
}

.rail-btn {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  color: #fff;
  text-shadow: 0 1px 6px rgba(0, 0, 0, 0.6);
}

.rail-icon {
  width: 46px;
  height: 46px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  background: rgba(22, 22, 28, 0.55);
}

.rail-btn.liked {
  color: var(--accent);
}

.rail-count {
  font-size: 12px;
}

.info {
  position: absolute;
  left: 24px;
  right: 96px;
  bottom: 28px;
  color: #fff;
  text-shadow: 0 1px 8px rgba(0, 0, 0, 0.7);
}

.author {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.avatar {
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: var(--accent);
  display: grid;
  place-items: center;
  font-weight: 700;
  font-size: 14px;
}

.username {
  font-weight: 600;
}

.title {
  font-size: 18px;
  margin: 0 0 6px;
}

.desc {
  margin: 0 0 8px;
  font-size: 14px;
  opacity: 0.9;
  max-width: 620px;
}

.meta {
  display: flex;
  gap: 14px;
  font-size: 13px;
  opacity: 0.8;
}

@media (max-width: 820px) {
  .info {
    left: 16px;
    right: 86px;
    bottom: 78px;
  }

  .rail {
    right: 12px;
    bottom: 120px;
  }
}
</style>
