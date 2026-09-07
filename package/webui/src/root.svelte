<script lang="ts">
import App from "./app.svelte";
import ErrorBoundary from "./components/error-boundary.svelte";
</script>

<ErrorBoundary label="app"><App /></ErrorBoundary>
