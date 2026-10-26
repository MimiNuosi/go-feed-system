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
  avatar_url?: string;
}

export interface Profile {
  user: Author;
  followers: number;
  following: number;
  videos: number;
  likes_received: number;
  is_following: boolean;
  is_me: boolean;
  avatar_url?: string;
}

export interface UserList {
  items: Author[];
  next_cursor?: string;
  has_more: boolean;
}

export interface ChunkInitResult {
  upload_id?: string;
  chunk_size: number;
  total_chunks: number;
  uploaded: number[];
  video_id?: number;
}

export interface ChunkStatus {
  upload_id: string;
  status: string;
  uploaded: number[];
  total_chunks: number;
  video_id?: number;
}

export interface AuthTokens {
  access_token: string;
  refresh_token: string;
  token_type: string;
  expires_at: string;
  refresh_expires_at: string;
}

const TOKEN_KEY = "feed.token";
const REFRESH_KEY = "feed.refresh";

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

export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_KEY);
}

export function setRefreshToken(token: string | null): void {
  if (token) {
    localStorage.setItem(REFRESH_KEY, token);
  } else {
    localStorage.removeItem(REFRESH_KEY);
  }
}

export class ApiError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

let refreshInFlight: Promise<boolean> | null = null;

async function tryRefresh(): Promise<boolean> {
  const refreshToken = getRefreshToken();
  if (!refreshToken) {
    return false;
  }
  if (refreshInFlight) {
    return refreshInFlight;
  }
  refreshInFlight = (async () => {
    try {
      const response = await fetch("/api/v1/auth/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });
      if (!response.ok) {
        return false;
      }
      const data = (await response.json()) as AuthTokens;
      setToken(data.access_token);
      setRefreshToken(data.refresh_token);
      return true;
    } catch {
      return false;
    } finally {
      refreshInFlight = null;
    }
  })();
  return refreshInFlight;
}

async function request<T>(path: string, init: RequestInit = {}, allowRefresh = true): Promise<T> {
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
    if (allowRefresh && !path.startsWith("/auth/")) {
      const refreshed = await tryRefresh();
      if (refreshed) {
        return request<T>(path, init, false);
      }
    }
    setToken(null);
    setRefreshToken(null);
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
    request<AuthTokens>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ email, password }),
    }, false),

  logout: (refreshToken: string) =>
    request<void>("/auth/logout", {
      method: "POST",
      body: JSON.stringify({ refresh_token: refreshToken }),
    }),

  changePassword: (oldPassword: string, newPassword: string) =>
    request<void>("/users/me/password", {
      method: "POST",
      body: JSON.stringify({ old_password: oldPassword, new_password: newPassword }),
    }),

  uploadAvatar: (file: File) => {
    const form = new FormData();
    form.set("file", file);
    return request<{ avatar_key: string }>("/users/me/avatar", {
      method: "POST",
      body: form,
    });
  },

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

  profile: (userId: number) => request<Profile>(`/users/${userId}/profile`),

  userFollowers: (userId: number, cursor?: string, pageSize = 20) =>
    request<UserList>(`/users/${userId}/followers?${pageQuery(pageSize, cursor)}`),

  userFollowing: (userId: number, cursor?: string, pageSize = 20) =>
    request<UserList>(`/users/${userId}/following?${pageQuery(pageSize, cursor)}`),

  likedVideos: (cursor?: string, pageSize = 12) =>
    request<Page>(`/users/me/liked-videos?${pageQuery(pageSize, cursor)}`),

  uploadVideo: (form: FormData) =>
    request<{ id: number; status: string }>("/videos", {
      method: "POST",
      body: form,
    }),

  initChunkUpload: (payload: {
    file_name: string;
    file_size: number;
    file_md5: string;
    content_type: string;
    title: string;
    description: string;
    chunk_size: number;
  }) =>
    request<ChunkInitResult>("/videos/chunk/init", {
      method: "POST",
      body: JSON.stringify(payload),
    }),

  uploadChunk: (uploadID: string, index: number, blob: Blob) => {
    const form = new FormData();
    form.set("upload_id", uploadID);
    form.set("chunk_index", String(index));
    form.set("file", blob, `chunk-${index}.part`);
    return request<{ received: number; total: number }>("/videos/chunk/upload", {
      method: "POST",
      body: form,
    });
  },

  chunkStatus: (uploadID: string) =>
    request<ChunkStatus>(`/videos/chunk/status?upload_id=${encodeURIComponent(uploadID)}`),

  completeChunkUpload: (uploadID: string) =>
    request<{ video_id: number }>("/videos/chunk/complete", {
      method: "POST",
      body: JSON.stringify({ upload_id: uploadID }),
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
