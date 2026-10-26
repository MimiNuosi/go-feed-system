<script setup lang="ts">
import { computed, ref } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useAuthStore, useToastStore } from "../stores";

const props = defineProps<{ register?: boolean }>();

const auth = useAuthStore();
const toast = useToastStore();
const route = useRoute();
const router = useRouter();

const username = ref("");
const email = ref("");
const password = ref("");
const busy = ref(false);

const isRegister = computed(() => Boolean(props.register));

async function submit() {
  busy.value = true;
  try {
    if (isRegister.value) {
      await auth.register(username.value.trim(), email.value.trim(), password.value);
      toast.push("注册成功");
    } else {
      await auth.login(email.value.trim(), password.value);
      toast.push("登录成功");
    }
    const redirect = typeof route.query.redirect === "string" ? route.query.redirect : "/";
    router.push(redirect);
  } catch (error) {
    toast.error(error);
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="auth">
    <form class="card" @submit.prevent="submit">
      <h1 class="brand">Feed</h1>
      <p class="muted sub">{{ isRegister ? "创建一个账号，开始发布作品" : "登录后点赞、评论、关注" }}</p>

      <label v-if="isRegister" class="field">
        <span>用户名</span>
        <input v-model="username" required minlength="3" maxlength="32" autocomplete="username" />
      </label>

      <label class="field">
        <span>邮箱</span>
        <input v-model="email" type="email" required autocomplete="email" />
      </label>

      <label class="field">
        <span>密码</span>
        <input
          v-model="password"
          type="password"
          required
          :minlength="isRegister ? 8 : undefined"
          autocomplete="current-password"
        />
      </label>

      <button class="btn primary submit" type="submit" :disabled="busy">
        {{ busy ? "处理中…" : isRegister ? "注册" : "登录" }}
      </button>

      <RouterLink :to="isRegister ? '/login' : '/register'" class="switch muted">
        {{ isRegister ? "已有账号？去登录" : "没有账号？去注册" }}
      </RouterLink>
    </form>
  </div>
</template>

<style scoped>
.auth {
  height: 100%;
  display: grid;
  place-items: center;
  padding: 24px;
}

.card {
  width: min(400px, 100%);
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 32px 28px;
  border: 1px solid var(--line);
  border-radius: 12px;
  background: var(--panel);
}

.brand {
  margin: 0;
  font-size: 30px;
  color: var(--accent);
  text-align: center;
}

.sub {
  margin: 0 0 6px;
  text-align: center;
  font-size: 13px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--muted);
}

.submit {
  margin-top: 6px;
  padding: 11px;
}

.switch {
  text-align: center;
  font-size: 13px;
}

.switch:hover {
  color: var(--text);
}
</style>
