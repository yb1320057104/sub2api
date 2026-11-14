<template><AuthorizationWorkspace :section="section" /></template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import AuthorizationWorkspace from '@/components/admin/account/AuthorizationWorkspace.vue'
const route = useRoute()
const section = computed(() => route.query.tab === 'proxies' ? 'proxies' : route.query.tab === 'history' ? 'history' : 'workbench')
</script>
