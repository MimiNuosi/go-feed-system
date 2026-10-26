<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import { UploadCloud } from "lucide-vue-next";
import { uploadWithProgress } from "../api";
import { useToastStore } from "../stores";

const router = useRouter();
const toast = useToastStore();
const file = ref<File | null>(null);
const title = ref("");
const description = ref("");
const progress = ref(0);
const uploading = ref(false);
const dragging = ref(false);

const canSubmit = computed(() => Boolean(file.value && title.value.trim() && !uploading.value));

function pick(event: Event) {
  const target = event.target as HTMLInputElement;
  file.value = target.files?.[0] ?? null;
}

function drop(event: DragEvent) {
  dragging.value = false;
  file.value = event.dataTransfer?.files?.[0] ?? null;
}

async function submit() {
  if (!file.value || !title.value.trim()) {
    return;
  }
  const form = new FormData();
  form.set("title", title.value.trim());
  form.set("description", description.value.trim());
  form.set("file", file.value);

  uploading.value = true;
  progress.value = 0;
  try {
    const created = await uploadWithProgress(form, (value) => {
      progress.value = value;
    });
    toast.push("上传成功");
    router.push(`/video/${created.id}`);
  } catch (error) {
    toast.error(error);
  } finally {
    uploading.value = false;
  }
}
</script>

<template>
  <section class="page">
    <h1 class="page-title">发布作品</h1>
    <p class="muted intro">支持 mp4 / mov / m4v / webm / 3gp，上传后会做内容嗅探校验。</p>

    <form class="form" @submit.prevent="submit">
      <label
        class="drop"
        :class="{ dragging }"
        @dragover.prevent="dragging = true"
        @dragleave.prevent="dragging = false"
        @drop.prevent="drop"
      >
        <UploadCloud :size="30" />
        <strong>{{ file ? file.name : "点击选择视频，或拖拽到这里" }}</strong>
        <span class="muted">{{ file ? `${(file.size / 1024 / 1024).toFixed(1)} MB` : "建议 9:16 竖屏视频" }}</span>
        <input type="file" accept="video/*" hidden @change="pick" />
      </label>

      <label class="field">
        <span>标题</span>
        <input v-model="title" maxlength="100" placeholder="给作品起个标题" />
      </label>

      <label class="field">
        <span>描述</span>
        <textarea v-model="description" rows="4" maxlength="1000" placeholder="补充说明（可选）" />
      </label>

      <div v-if="uploading" class="progress">
        <div class="bar" :style="{ width: `${progress}%` }" />
        <span>{{ progress }}%</span>
      </div>

      <button class="btn primary submit" type="submit" :disabled="!canSubmit">
        {{ uploading ? "上传中…" : "发布" }}
      </button>
    </form>
  </section>
</template>

<style scoped>
.page {
  max-width: 720px;
  margin: 0 auto;
  padding: 32px 24px 60px;
}

.intro {
  margin-top: -8px;
  margin-bottom: 20px;
  font-size: 13px;
}

.form {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.drop {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  padding: 40px 20px;
  border: 1px dashed #3a3a46;
  border-radius: var(--radius);
  background: var(--panel);
  cursor: pointer;
  text-align: center;
}

.drop.dragging {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 13px;
  color: var(--muted);
}

.progress {
  position: relative;
  height: 26px;
  border-radius: 6px;
  background: var(--panel-2);
  overflow: hidden;
}

.bar {
  height: 100%;
  background: var(--accent);
  transition: width 0.2s ease;
}

.progress span {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  font-size: 12px;
}

.submit {
  align-self: flex-start;
  padding: 10px 28px;
}
</style>
