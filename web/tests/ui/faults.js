(() => {
	if (!window.reviewOriginalSend) {
		window.reviewOriginalSend = WebSocket.prototype.send;
		window.reviewFailures = new Set();
		window.reviewFrames = [];
		WebSocket.prototype.send = function (frame) {
			const data = typeof frame === "string" ? JSON.parse(frame) : {};
			window.reviewSocket = this;
			if (data.method) window.reviewFrames.push({ method: data.method, id: data.id });
			if (window.reviewFailures.has(data.method)) {
				queueMicrotask(() =>
					this.dispatchEvent(
						new MessageEvent("message", {
							data: JSON.stringify({
								id: data.id,
								ok: false,
								error: "Synthetic review: connection failed before acceptance",
							}),
						}),
					),
				);
				return;
			}
			return window.reviewOriginalSend.call(this, frame);
		};
	}
	return true;
})();
