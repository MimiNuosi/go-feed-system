export interface Author {
  id: number;
  username: string;
}

export interface FeedItem {
  id: number;
  author: Author;
  title: string;
  description: string;
  content_type: string;
  created_at: string;
  view_count: number;
  like_count: number;
  is_liked_by: boolean;
}

export interface Page {
  items: FeedItem[];
  next_cursor?: string;
  has_more: boolean;
}

export interface VideoDetail {
  id: number;
  author: Author;
  title: string;
  description: string;
  content_type: string;
  size_bytes: number;
  view_count: number;
  status: string;
  like_count: number;
  is_liked_by: boolean;
}

export interface CommentItem {
  id: number;
  video_id: number;
  user_id: number;
  username: string;
  content: string;
  created_at: string;
}

export interface CommentPage {
  items: CommentItem[];
  next_cursor?: string;
  has_more: boolean;
}

export interface User {
  id: number;
  username: string;
  email: string;
  created_at: string;
}

const TOKEN_KEY = "feed.token";

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}

export function setToken(token: string | null): void {
  if (token) {
    localStorage.setItem(TOKEN_KEY, token);
  } else {
    localStorage.removeItem(TOKEN_KEY);
  }
}

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  const token = getToken();
  if (token) {
    headers.set("Authorization", `Bearer ${token}`);
  }
  if (init.body && !(init.body instanceof FormData)) {
    headers.set("Content-Type", "application/json");
  }

  const response = await fetch(`/api/v1${path}`, { ...init, headers });
  if (response.status === 401) {
    setToken(null);
    window.dispatchEvent(new CustomEvent("auth:expired"));
  }
  if (!response.ok) {
    let message = `请求失败（${response.status}）`;
    try {
      const data = (await response.json()) as {
        error?: { message?: string } | string;
        message?: string;
      };
      if (typeof data.error === "string") {
        message = data.error;
      } else if (data.error?.message) {
        message = data.error.message;
      } else if (data.message) {
        message = data.message;
      }
    } catch {
      // 非 JSON 响应，保留默认提示
    }
    throw new ApiError(response.status, message);
  }
  if (response.status === 204) {
    return undefined as T;
  }
  return (await response.json()) as T;
}

function pageQuery(pageSize: number, cursor?: string): string {
  const params = new URLSearchParams({ page_size: String(pageSize) });
  if (cursor) {
    params.set("cursor", cursor);
  }
  return params.toString();
}

export const api = {
  register: (username: string, email: string, password: string) =>
    request<{ user: User }>("/auth/register", {
      method: "POST",
      body: JSON.stringify({ username, email, password }),
    }),

  login: (email: string, password: string) =>
    request<{ access_token: string; expires_at: string }>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }),

  me: () => request<User>("/users/me"),

  latest: (cursor?: string, pageSize = 6) =>
    request<Page>(`/feed/latest?${pageQuery(pageSize, cursor)}`),

  following: (cursor?: string, pageSize = 6) =>
    request<Page>(`/feed/following?${pageQuery(pageSize, cursor)}`),

  hot: (windowName = "1h", pageSize = 30) =>
    request<Page>(`/feed/hot?window=${windowName}&page_size=${pageSize}`),

  video: (id: number) => request<VideoDetail>(`/videos/${id}`),

  like: (id: number, shouldLike: boolean) =>
    request<void>(`/videos/${id}/like`, { method: shouldLike ? "POST" : "DELETE" }),

  view: (id: number) => request<void>(`/videos/${id}/view`, { method: "POST" }),

  follow: (userId: number, shouldFollow: boolean) =>
    request<void>(`/users/${userId}/follow`, {
      method: shouldFollow ? "POST" : "DELETE",
    }),

  followStatus: (userId: number) =>
    request<{ following: boolean }>(`/users/${userId}/follow/status`),

  comments: (videoId: number, cursor?: string, pageSize = 30) => {
    const params = new URLSearchParams({ page_size: String(pageSize) });
    if (cursor) {
      params.set("cursor", cursor);
    }
    return request<CommentPage>(`/videos/${videoId}/comments?${params.toString()}`);
  },

  addComment: (videoId: number, content: string) =>
    request<CommentItem>(`/videos/${videoId}/comments`, {
      method: "POST",
      body: JSON.stringify({ content }),
    }),

  deleteComment: (commentId: number) =>
    request<void>(`/comments/${commentId}`, { method: "DELETE" }),

  myVideos: (userId: number, cursor?: string, pageSize = 12) => {
    const params = new URLSearchParams({ page_size: String(pageSize) });
    if (cursor) {
      params.set("cursor", cursor);
    }
    return request<Page>(`/users/${userId}/videos?${params.toString()}`);
  },

  uploadVideo: (form: FormData) =>
    request<{ id: number; status: string }>("/videos", {
      method: "POST",
      body: form,
    }),
};

/** 上传进度需要 XHR（fetch 拿不到上传进度）。 */
export function uploadWithProgress(
  form: FormData,
  onProgress: (percent: number) => void,
): Promise<{ id: number; status: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/v1/videos");
    const token = getToken();
    if (token) {
      xhr.setRequestHeader("Authorization", `Bearer ${token}`);
    }
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) {
        onProgress(Math.round((event.loaded / event.total) * 100));
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(JSON.parse(xhr.responseText) as { id: number; status: string });
      } else {
        reject(new ApiError(xhr.status, `上传失败（${xhr.status}）`));
      }
    };
    xhr.onerror = () => reject(new ApiError(0, "网络异常，上传中断"));
    xhr.send(form);
  });
}

export function videoFileURL(id: number): string {
  return `/api/v1/videos/${id}/file`;
}
