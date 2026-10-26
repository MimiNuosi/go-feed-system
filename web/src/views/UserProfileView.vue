<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { api, type Author, type FeedItem, type Profile } from "../api";
import { useAuthStore, useToastStore } from "../stores";

const route = useRoute();
const router = useRouter();
const auth = useAuthStore();
const toast = useToastStore();

type Tab = "videos" | "followers" | "following" | "liked";

const userId = computed(() => Number(route.params.id));
const profile = ref<Profile | null>(null);
const videos = ref<FeedItem[]>([]);
const liked = ref<FeedItem[]>([]);
const followers = ref<Author[]>([]);
const following = ref<Author[]>([]);
const tab = ref<Tab>("videos");
const loading = ref(false);
const followBusy = ref(false);

const tabs = computed(() => {
  const base: Array<{ key: Tab; label: string; count?: number }> = [
    { key: "videos", label: "作品", count: profile.value?.videos },
    { key: "followers", label: "粉丝", count: profile.value?.followers },
    { key: "following", label: "关注", count: profile.value?.following },
  ];
  if (profile.value?.is_me) {
    base.push({ key: "liked", label: "喜欢" });
  }
  return base;
});

async function loadProfile() {
  loading.value = true;
  try {
    profile.value = await api.profile(userId.value);
    const page = await api.myVideos(userId.value, undefined, 60);
    videos.value = page.items;
  } catch (error) {
    toast.error(error);
    profile.value = null;
  } finally {
    loading.value = false;
  }
}

async function loadTab(next: Tab) {
  tab.value = next;
  try {
    if (next === "followers" && !followers.value.length) {
      followers.value = (await api.userFollowers(userId.value, undefined, 50)).items;
    } else if (next === "following" && !following.value.length) {
      following.value = (await api.userFollowing(userId.value, undefined, 50)).items;
    } else if (next === "liked" && !liked.value.length) {
      liked.value = (await api.likedVideos(undefined, 60)).items;
    }
  } catch (error) {
    toast.error(error);
  }
}

async function toggleFollow() {
  if (!profile.value) {
    return;
  }
  if (!auth.isLoggedIn) {
    toast.push("登录后才能关注");
    void router.push({ name: "login", query: { redirect: route.fullPath } });
    return;
  }
  followBusy.value = true;
  try {
    await api.follow(profile.value.user.id, !profile.value.is_following);
    profile.value.is_following = !profile.value.is_following;
    profile.value.followers += profile.value.is_following ? 1 : -1;
    toast.push(profile.value.is_following ? "已关注" : "已取消关注");
  } catch (error) {
    toast.error(error);
  } finally {
    followBusy.value = false;
  }
}

watch(
  () => route.params.id,
  () => {
    followers.value = [];
    following.value = [];
    liked.value = [];
    tab.value = "videos";
    void loadProfile();
  },
);

onMounted(loadProfile);
</script>

<template>
  <section class="page">
    <p v-if="loading && !profile" class="muted state">加载中…</p>
    <p v-else-if="!profile" class="muted state">用户不存在</p>

    <template v-else>
      <header class="header">
        <img
          v-if="profile.avatar_url"
          :src="profile.avatar_url"
          alt="头像"
          class="avatar"
        />
        <div v-else class="avatar">{{ profile.user.username.slice(0, 1).toUpperCase() }}</div>
        <div class="who">
          <h1>{{ profile.user.username }}</h1>
          <div class="stats">
            <span><b>{{ profile.videos }}</b> 作品</span>
            <span><b>{{ profile.followers }}</b> 粉丝</span>
            <span><b>{{ profile.following }}</b> 关注</span>
            <span><b>{{ profile.likes_received }}</b> 获赞</span>
          </div>
        </div>
        <button
          v-if="!profile.is_me"
          class="btn"
          :class="{ primary: !profile.is_following }"
          :disabled="followBusy"
          @click="toggleFollow"
        >
          {{ profile.is_following ? "已关注" : "关注" }}
        </button>
        <RouterLink v-else to="/upload" class="btn primary">发布作品</RouterLink>
      </header>

      <nav class="tabs">
        <button
          v-for="item in tabs"
          :key="item.key"
          :class="{ on: tab === item.key }"
          @click="loadTab(item.key)"
        >
          {{ item.label }}
          <span v-if="item.count !== undefined" class="count">{{ item.count }}</span>
        </button>
      </nav>

      <div v-if="tab === 'videos'" class="grid">
        <RouterLink v-for="item in videos" :key="item.id" :to="`/video/${item.id}`" class="tile">
          <div class="thumb">▶</div>
          <div class="tile-title">{{ item.title }}</div>
          <div class="tile-meta">{{ item.view_count }} 播放 · {{ item.like_count }} 点赞</div>
        </RouterLink>
        <p v-if="!videos.length" class="muted state">还没有作品</p>
      </div>

      <div v-else-if="tab === 'liked'" class="grid">
        <RouterLink v-for="item in liked" :key="item.id" :to="`/video/${item.id}`" class="tile">
          <div class="thumb">▶</div>
          <div class="tile-title">{{ item.title }}</div>
          <div class="tile-meta">@{{ item.author.username }}</div>
        </RouterLink>
        <p v-if="!liked.length" class="muted state">还没有赞过视频</p>
      </div>

      <ul v-else class="user-list">
        <li v-for="item in tab === 'followers' ? followers : following" :key="item.id">
          <RouterLink :to="`/u/${item.id}`" class="user-row">
            <span class="mini-avatar">{{ item.username.slice(0, 1).toUpperCase() }}</span>
            <span class="user-name">{{ item.username }}</span>
            <span class="muted">查看主页</span>
          </RouterLink>
        </li>
        <p
          v-if="!(tab === 'followers' ? followers : following).length"
          class="muted state"
        >
          {{ tab === "followers" ? "还没有粉丝" : "还没有关注任何人" }}
        </p>
      </ul>
    </template>
  </section>
</template>

<style scoped>
.page {
  max-width: 1000px;
  margin: 0 auto;
  padding: 32px 24px 60px;
}

.header {
  display: flex;
  align-items: center;
  gap: 18px;
  padding-bottom: 20px;
  border-bottom: 1px solid var(--line);
}

.avatar {
  width: 72px;
  height: 72px;
  border-radius: 50%;
  object-fit: cover;
  background: var(--accent);
  display: grid;
  place-items: center;
  font-size: 30px;
  font-weight: 700;
}

.who {
  flex: 1;
  min-width: 0;
}

.who h1 {
  margin: 0 0 8px;
  font-size: 22px;
}

.stats {
  display: flex;
  gap: 18px;
  font-size: 13px;
  color: var(--muted);
}

.stats b {
  color: var(--text);
  font-size: 15px;
  margin-right: 3px;
}

.tabs {
  display: flex;
  gap: 8px;
  padding: 16px 0;
}

.tabs button {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 8px;
  color: var(--muted);
  font-size: 14px;
}

.tabs button.on {
  background: var(--accent-soft);
  color: var(--text);
}

.count {
  font-size: 12px;
  opacity: 0.7;
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

.user-list {
  list-style: none;
  margin: 0;
  padding: 0;
  border-top: 1px solid var(--line);
}

.user-list li {
  border-bottom: 1px solid var(--line);
}

.user-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 14px 4px;
}

.mini-avatar {
  width: 36px;
  height: 36px;
  border-radius: 50%;
  background: #2a2a34;
  display: grid;
  place-items: center;
  font-weight: 600;
}

.user-name {
  flex: 1;
  font-weight: 600;
}

.state {
  padding: 40px 0;
  text-align: center;
}

@media (max-width: 820px) {
  .page {
    padding: 18px 16px 80px;
  }

  .header {
    flex-wrap: wrap;
  }

  .stats {
    flex-wrap: wrap;
    gap: 10px;
  }
}
</style>
