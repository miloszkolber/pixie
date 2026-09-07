import { type LoginFrame, safeBrowserURL } from "@pixie/contracts";

export interface LoginInputSelect {
	kind: "select";
	message: string;
	options: { id: string; label: string }[];
}
export interface LoginInputPrompt {
	kind: "prompt";
	message: string;
	placeholder?: string;
	allowEmpty?: boolean;
	secret?: boolean;
}
export type LoginInput = LoginInputSelect | LoginInputPrompt;

export interface LoginState {
	loginId: string;
	providerId: string;
	status: "active" | "success" | "error";
	url?: string;
	instructions?: string;
	deviceCode?: { userCode: string; verificationUri: string; expiresInSeconds?: number };
	input?: LoginInput;
	progress?: string;
	error?: string;
}

export function newLoginState(loginId: string, providerId: string): LoginState {
	return { loginId, providerId, status: "active" };
}

export function foldLoginFrame(state: LoginState, frame: LoginFrame): LoginState {
	switch (frame.kind) {
		case "authUrl": {
			const url = safeBrowserURL(frame.url);
			if (!url) return { ...state, status: "error", error: "Pi returned an invalid sign-in URL." };
			return {
				...state,
				url,
				...(frame.instructions ? { instructions: frame.instructions } : {}),
			};
		}
		case "deviceCode": {
			const verificationUri = safeBrowserURL(frame.verificationUri);
			if (!verificationUri)
				return { ...state, status: "error", error: "Pi returned an invalid sign-in URL." };
			return {
				...state,
				deviceCode: {
					userCode: frame.userCode,
					verificationUri,
					...(frame.expiresInSeconds ? { expiresInSeconds: frame.expiresInSeconds } : {}),
				},
			};
		}
		case "select": {
			const { progress: _p, ...rest } = state;
			return { ...rest, input: { kind: "select", message: frame.message, options: frame.options } };
		}
		case "prompt": {
			const { progress: _p, ...rest } = state;
			return {
				...rest,
				input: {
					kind: "prompt",
					message: frame.message,
					...(frame.placeholder ? { placeholder: frame.placeholder } : {}),
					...(frame.allowEmpty ? { allowEmpty: true } : {}),
					...(frame.secret ? { secret: true } : {}),
				},
			};
		}
		case "progress": {
			const { input: _input, ...rest } = state;
			return { ...rest, progress: frame.message };
		}
		case "success": {
			const { input: _i, progress: _p, ...rest } = state;
			return { ...rest, status: "success" };
		}
		case "error": {
			const { input: _i, progress: _p, ...rest } = state;
			return { ...rest, status: "error", error: frame.message };
		}
	}
}
