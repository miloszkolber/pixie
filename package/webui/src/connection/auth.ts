export interface AuthStatus {
	authenticationEnabled: boolean;
	authenticated: boolean;
}

export async function authRequest(path: string, body?: Record<string, string>): Promise<Response> {
	const init: RequestInit = {
		method: body ? "POST" : "GET",
		credentials: "same-origin",
		cache: "no-store",
	};
	if (body) {
		init.headers = { "content-type": "application/json" };
		init.body = JSON.stringify(body);
	}
	return fetch(path, init);
}

export async function logoutController(): Promise<void> {
	await authRequest("/auth/logout", {});
}
