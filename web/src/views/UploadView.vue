<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import { UploadCloud } from "lucide-vue-next";
import SparkMD5 from "spark-md5";
import { api, uploadWithProgress } from "../api";
import { useToastStore } from "../stores";

const CHUNK_SIZE = 5 * 1024 * 1024;
const MD5_SLICE = 2 * 1024 * 1024;
const CONCURRENCY = 3;
const RETRIES = 3;

const router = useRouter();
const toast = useToastStore();

const mode = ref<"chunk" | "single">("chunk");
const file = ref<File | null>(null);
const title = ref("");
const description = ref("");
const progress = ref(0);
const phase = ref("");
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

function chunkLength(fileSize: number, chunkSize: number, total: number, index: number): number {
  if (index < total - 1) {
    return chunkSize;
  }
  return fileSize - chunkSize * (total - 1);
}

function computeMD5(target: File, onProgress: (percent: number) => void): Promise<string> {
  return new Promise((resolve, reject) => {
    const spark = new SparkMD5.ArrayBuffer();
    const reader = new FileReader();
    let offset = 0;

    const readNext = () => {
      if (offset >= target.size) {
        resolve(spark.end());
        return;
      }
      const blob = target.slice(offset, Math.min(offset + MD5_SLICE, target.size));
      reader.onload = () => {
        spark.append(reader.result as ArrayBuffer);
        offset += blob.size;
        onProgress((offset / target.size) * 100);
        readNext();
      };
      reader.onerror = () => reject(new Error("读取文件失败"));
      reader.readAsArrayBuffer(blob);
    };
    readNext();
  });
}

async function retry<T>(task: () => Promise<T>, attempts: number): Promise<T> {
  let lastError: unknown;
  for (let attempt = 0; attempt < attempts; attempt++) {
    try {
      return await task();
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => window.setTimeout(resolve, 500 * (attempt + 1)));
    }
  }
  throw lastError;
}

async function uploadChunked(target: File) {
  phase.value = "计算文件 MD5…";
  progress.value = 0;
  const md5 = await computeMD5(target, (percent) => {
    progress.value = Math.round(percent * 0.2);
  });

  phase.value = "创建上传会话…";
  const init = await api.initChunkUpload({
    file_name: target.name,
    file_size: target.size,
    file_md5: md5,
    content_type: target.type || "video/mp4",
    title: title.value.trim(),
    description: description.value.trim(),
    chunk_size: CHUNK_SIZE,
  });

  // 秒传：同一作者已经上传过相同 MD5 的文件。
  if (init.video_id) {
    progress.value = 100;
    phase.value = "秒传完成";
    toast.push("检测到相同文件，已秒传");
    router.push(`/video/${init.video_id}`);
    return;
  }
  if (!init.upload_id) {
    throw new Error("创建上传会话失败");
  }

  const uploadID = init.upload_id;
  const total = init.total_chunks;
  const uploaded = new Set(init.uploaded);
  let uploadedBytes = 0;
  uploaded.forEach((index) => {
    uploadedBytes += chunkLength(target.size, init.chunk_size, total, index);
  });

  // 断点续传：只传服务端还没有的分片。
  const pending: number[] = [];
  for (let index = 0; index < total; index++) {
    if (!uploaded.has(index)) {
      pending.push(index);
    }
  }

  let cursor = 0;
  const workers = Array.from({ length: Math.min(CONCURRENCY, Math.max(pending.length, 1)) }, async () => {
    while (cursor < pending.length) {
      const index = pending[cursor++];
      const start = index * init.chunk_size;
      const blob = target.slice(start, Math.min(start + init.chunk_size, target.size));
      await retry(() => api.uploadChunk(uploadID, index, blob), RETRIES);
      uploadedBytes += blob.size;
      uploaded.add(index);
      progress.value = 20 + Math.round((uploadedBytes / target.size) * 65);
      phase.value = `上传分片 ${uploaded.size}/${total}`;
    }
  });
  await Promise.all(workers);

  phase.value = "服务端合并分片…";
  progress.value = 90;
  const done = await api.completeChunkUpload(uploadID);
  progress.value = 100;
  phase.value = "完成";
  toast.push("上传成功");
  router.push(`/video/${done.video_id}`);
}

async function uploadSingle(target: File) {
  const form = new FormData();
  form.set("title", title.value.trim());
  form.set("description", description.value.trim());
  form.set("file", target);

  phase.value = "上传中…";
  const created = await uploadWithProgress(form, (percent) => {
    progress.value = percent;
  });
  progress.value = 100;
  phase.value = "完成";
  toast.push("上传成功");
  router.push(`/video/${created.id}`);
}

async function submit() {
  const target = file.value;
  if (!target || !title.value.trim()) {
    return;
  }
  uploading.value = true;
  progress.value = 0;
  try {
    if (mode.value === "chunk") {
      await uploadChunked(target);
    } else {
      await uploadSingle(target);
    }
  } catch (error) {
    toast.error(error);
    phase.value = "上传失败";
  } finally {
    uploading.value = false;
  }
}
</script>

<template>
  <section class="page">
    <h1 class="page-title">发布作品</h1>
    <p class="muted intro">
      分片上传支持 5MB 切片、MD5 秒传与断点续传；普通上传走整文件 multipart。
    </p>

    <div class="seg">
      <button :class="{ on: mode === 'chunk' }" @click="mode = 'chunk'">分片上传（推荐）</button>
      <button :class="{ on: mode === 'single' }" @click="mode = 'single'">普通上传</button>
    </div>

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
        <span class="muted">
          {{ file ? `${(file.size / 1024 / 1024).toFixed(1)} MB` : "支持 mp4 / mov / m4v / webm / 3gp" }}
        </span>
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

      <div v-if="uploading || progress > 0" class="progress">
        <div class="bar" :style="{ width: `${progress}%` }" />
        <span>{{ phase }} {{ progress }}%</span>
      </div>

      <button class="btn primary submit" type="submit" :disabled="!canSubmit">
        {{ uploading ? "处理中…" : "发布" }}
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
  margin-bottom: 16px;
  font-size: 13px;
}

.seg {
  display: inline-flex;
  border: 1px solid var(--line);
  border-radius: 8px;
  overflow: hidden;
  margin-bottom: 16px;
}

.seg button {
  padding: 8px 14px;
  font-size: 13px;
  color: var(--muted);
}

.seg button.on {
  background: var(--accent-soft);
  color: var(--text);
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
  height: 28px;
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
