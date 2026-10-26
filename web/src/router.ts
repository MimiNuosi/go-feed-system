import { createRouter, createWebHistory } from "vue-router";
import { useAuthStore } from "./stores";

const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: "/",
      name: "latest",
      component: () => import("./views/FeedView.vue"),
      props: { mode: "latest" },
    },
    {
      path: "/following",
      name: "following",
      component: () => import("./views/FeedView.vue"),
      props: { mode: "following" },
      meta: { requiresAuth: true },
    },
    { path: "/hot", name: "hot", component: () => import("./views/HotView.vue") },
    {
      path: "/video/:id",
      name: "video",
      component: () => import("./views/VideoDetailView.vue"),
    },
    {
      path: "/upload",
      name: "upload",
      component: () => import("./views/UploadView.vue"),
      meta: { requiresAuth: true },
    },
    {
      path: "/me",
      name: "me",
      component: () => import("./views/MeView.vue"),
      meta: { requiresAuth: true },
    },
    {
      path: "/u/:id",
      name: "user",
      component: () => import("./views/UserProfileView.vue"),
    },
    { path: "/login", name: "login", component: () => import("./views/AuthView.vue") },
    {
      path: "/register",
      name: "register",
      component: () => import("./views/AuthView.vue"),
      props: { register: true },
    },
    { path: "/:pathMatch(.*)*", redirect: "/" },
  ],
});

router.beforeEach((to) => {
  const auth = useAuthStore();
  if (to.meta.requiresAuth && !auth.isLoggedIn) {
    return { name: "login", query: { redirect: to.fullPath } };
  }
  return true;
});

export default router;
