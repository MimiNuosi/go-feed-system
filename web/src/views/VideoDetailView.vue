<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { ArrowLeft, Heart, MessageCircle, Volume2, VolumeX } from "lucide-vue-next";
import { api, videoFileURL, type VideoDetail } from "../api";
import { useAuthStore, useToastStore } from "../stores";
import CommentPanel from "../components/CommentPanel.vue";

const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const toast = useToastStore();

const video = ref<VideoDetail | null>(null);
const videoEl = ref<HTMLVideoElement | null>(null);
const muted = ref(false);
const liked = ref(false);
const likeCount = ref(0);
const following = ref(false);
const followBusy = ref(false);
const showComments = ref(true);

let viewTimer: number | undefined;

const id = Number(route.params.id);

async function load() {
  try {
    const detail = await api.video(id);
    video.value = detail;
    liked.value = detail.is_liked_by;
    likeCount.value = detail.like_count;
    if (auth.isLoggedIn && auth.user?.id !== detail.author.id) {
      const status = await api.followStatus(detail.author.id).catch(() => ({ following: false }));
      following.value = status.following;
    }
  } catch (error) {
    toast.error(error);
  }
}

async function toggleLike() {
  if (!auth.isLoggedIn) {
    toast.push("登录后才能点赞");
    void router.push({ name: "login", query: { redirect: route.fullPath } });
    return;
  }
  const next = !liked.value;
  try {
    await api.like(id, next);
    liked.value = next;
    likeCount.value = Math.max(0, likeCount.value + (next ? 1 : -1));
  } catch (error) {
    toast.error(error);
  }
}

async function toggleFollow() {
  if (!video.value) {
    return;
  }
  if (!auth.isLoggedIn) {
    toast.push("登录后才能关注");
    void router.push({ name: "login", query: { redirect: route.fullPath } });
    return;
  }
  followBusy.value = true;
  try {
    await api.follow(video.value.author.id, !following.value);
    following.value = !following.value;
    toast.push(following.value ? "已关注" : "已取消关注");
  } catch (error) {
    toast.error(error);
  } finally {
    followBusy.value = false;
  }
}

watch(
  () => videoEl.value,
  (el) => {
    if (!el) {
      return;
    }
    void el.play().catch(() => undefined);
    viewTimer = window.setTimeout(() => {
      void api.view(id).catch(() => undefined);
    }, 3000);
  },
);

onMounted(load);

onUnmounted(() => {
  if (viewTimer !== undefined) {
    window.clearTimeout(viewTimer);
  }
});
</script>

<template>
  <div class="detail">
    <button class="back" @click="router.back()"><ArrowLeft :size="18" /> 返回</button>

    <template v-if="video">
      <section class="player">
        <video
          ref="videoEl"
          :src="videoFileURL(video.id)"
          controls
          loop
          playsinline
          :muted="muted"
        />
        <button class="mute" @click="muted = !muted">
          <VolumeX v-if="muted" :size="16" />
          <Volume2 v-else :size="16" />
          {{ muted ? "已静音" : "声音开" }}
        </button>
      </section>

      <section class="side" :class="{ wide: showComments }">
        <div class="meta">
          <h1 class="title">{{ video.title }}</h1>
          <p v-if="video.description" class="desc">{{ video.description }}</p>
          <div class="stats">
            <span>{{ video.view_count }} 播放</span>
            <span>{{ likeCount }} 点赞</span>
            <span>{{ video.content_type }}</span>
          </div>

          <div class="author">
            <span class="avatar">{{ video.author.username.slice(0, 1).toUpperCase() }}</span>
            <div class="who">
              <strong>{{ video.author.username }}</strong>
              <span class="muted">作者</span>
            </div>
            <button
              v-if="auth.user?.id !== video.author.id"
              class="btn"
              :disabled="followBusy"
              @click="toggleFollow"
            >
              {{ following ? "已关注" : "关注" }}
            </button>
          </div>

          <div class="ops">
            <button class="btn" :class="{ liked }" @click="toggleLike">
              <Heart :size="16" :fill="liked ? 'currentColor' : 'none'" /> {{ liked ? "取消点赞" : "点赞" }}
            </button>
            <button class="btn" @click="showComments = !showComments">
              <MessageCircle :size="16" /> {{ showComments ? "收起评论" : "查看评论" }}
            </button>
          </div>
        </div>

        <CommentPanel v-if="showComments" :video-id="video.id" class="panel" @close="showComments = false" />
      </section>
    </template>

    <p v-else class="loading muted">加载中…</p>
  </div>
</template>

<style scoped>
.detail {
  position: relative;
  display: flex;
  height: 100%;
  background: #050507;
}

.back {
  position: absolute;
  top: 16px;
  left: 16px;
  z-index: 5;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 12px;
  border-radius: 8px;
  background: rgba(20, 20, 26, 0.8);
  font-size: 13px;
}

.player {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: #000;
  position: relative;
}

.player video {
  max-width: 100%;
  max-height: 100%;
  height: 100%;
  object-fit: contain;
}

.mute {
  position: absolute;
  right: 16px;
  bottom: 16px;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 7px 12px;
  border-radius: 8px;
  background: rgba(20, 20, 26, 0.8);
  font-size: 12px;
}

.side {
  width: 380px;
  flex: 0 0 380px;
  display: flex;
  flex-direction: column;
  border-left: 1px solid var(--line);
  background: var(--panel);
}

.meta {
  padding: 56px 20px 18px;
  border-bottom: 1px solid var(--line);
}

.title {
  font-size: 20px;
  margin: 0 0 8px;
}

.desc {
  color: var(--muted);
  font-size: 14px;
  line-height: 1.6;
  margin: 0 0 12px;
  white-space: pre-wrap;
}

.stats {
  display: flex;
  gap: 14px;
  font-size: 12px;
  color: var(--muted);
  margin-bottom: 16px;
}

.author {
  display: flex;
  align-items: center;
  gap: 10px;
}

.avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: var(--accent);
  display: grid;
  place-items: center;
  font-weight: 700;
}

.who {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.ops {
  display: flex;
  gap: 10px;
  margin-top: 16px;
}

.btn.liked {
  color: var(--accent);
  border-color: rgba(254, 44, 85, 0.5);
}

.panel {
  flex: 1;
  min-height: 0;
  border-left: none;
}

.loading {
  margin: auto;
}

@media (max-width: 900px) {
  .detail {
    flex-direction: column;
    overflow-y: auto;
    padding-bottom: 64px;
  }

  .player {
    height: 52vh;
    flex: none;
  }

  .side {
    width: 100%;
    flex: 1;
    border-left: none;
  }

  .meta {
    padding-top: 20px;
  }

  .panel {
    min-height: 300px;
  }
}
</style>
