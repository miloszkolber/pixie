<script module lang="ts">
export type IconName =
	| "app-window"
	| "archive"
	| "archive-restore"
	| "arrow-down"
	| "arrow-up"
	| "book-open"
	| "bot"
	| "boxes"
	| "brain"
	| "brain-circuit"
	| "camera"
	| "check"
	| "chevron-down"
	| "chevron-left"
	| "chevron-right"
	| "chevron-up"
	| "circle"
	| "circle-alert"
	| "circle-dot"
	| "clock"
	| "clock-arrow-left"
	| "code-xml"
	| "copy"
	| "corner-up-right"
	| "ellipsis"
	| "external-link"
	| "eye"
	| "eye-off"
	| "file"
	| "file-diff"
	| "file-plus"
	| "file-text"
	| "flask-conical"
	| "fold-vertical"
	| "folder"
	| "folder-open"
	| "gauge"
	| "git-branch"
	| "git-fork"
	| "globe"
	| "house"
	| "image"
	| "info"
	| "key-round"
	| "layers"
	| "lightbulb"
	| "link"
	| "list-checks"
	| "loader-circle"
	| "lock"
	| "log-in"
	| "log-out"
	| "message-circle-question"
	| "message-square"
	| "message-square-plus"
	| "octagon-alert"
	| "paperclip"
	| "pencil"
	| "pilcrow"
	| "plus"
	| "puzzle"
	| "refresh-cw"
	| "rocket"
	| "rotate-ccw"
	| "rotate-cw"
	| "save"
	| "search"
	| "settings"
	| "settings-2"
	| "skip-forward"
	| "sparkles"
	| "square"
	| "target"
	| "trash-2"
	| "triangle-alert"
	| "type"
	| "wrench"
	| "x";
</script>

<script lang="ts">
	import appWindow from "../../vendor/mewa-icons/icons/app-window.svg";
	import archiveRestore from "../../vendor/mewa-icons/icons/archive-restore.svg";
	import archive from "../../vendor/mewa-icons/icons/archive.svg";
	import arrowDown from "../../vendor/mewa-icons/icons/arrow-down.svg";
	import arrowUp from "../../vendor/mewa-icons/icons/arrow-up.svg";
	import bookOpen from "../../vendor/mewa-icons/icons/book-open.svg";
	import bot from "../../vendor/mewa-icons/icons/bot.svg";
	import boxes from "../../vendor/mewa-icons/icons/boxes.svg";
	import brainCircuit from "../../vendor/mewa-icons/icons/brain-circuit.svg";
	import brain from "../../vendor/mewa-icons/icons/brain.svg";
	import camera from "../../vendor/mewa-icons/icons/camera.svg";
	import check from "../../vendor/mewa-icons/icons/check.svg";
	import chevronDown from "../../vendor/mewa-icons/icons/chevron-down.svg";
	import chevronLeft from "../../vendor/mewa-icons/icons/chevron-left.svg";
	import chevronRight from "../../vendor/mewa-icons/icons/chevron-right.svg";
	import chevronUp from "../../vendor/mewa-icons/icons/chevron-up.svg";
	import circleAlert from "../../vendor/mewa-icons/icons/circle-alert.svg";
	import circleDot from "../../vendor/mewa-icons/icons/circle-dot.svg";
	import circle from "../../vendor/mewa-icons/icons/circle.svg";
	import clockArrowLeft from "../../vendor/mewa-icons/icons/clock-arrow-left.svg";
	import clock from "../../vendor/mewa-icons/icons/clock.svg";
	import codeXml from "../../vendor/mewa-icons/icons/code-xml.svg";
	import copy from "../../vendor/mewa-icons/icons/copy.svg";
	import cornerUpRight from "../../vendor/mewa-icons/icons/corner-up-right.svg";
	import ellipsis from "../../vendor/mewa-icons/icons/ellipsis.svg";
	import externalLink from "../../vendor/mewa-icons/icons/external-link.svg";
	import eyeOff from "../../vendor/mewa-icons/icons/eye-off.svg";
	import eye from "../../vendor/mewa-icons/icons/eye.svg";
	import fileDiff from "../../vendor/mewa-icons/icons/file-diff.svg";
	import filePlus from "../../vendor/mewa-icons/icons/file-plus.svg";
	import fileText from "../../vendor/mewa-icons/icons/file-text.svg";
	import file from "../../vendor/mewa-icons/icons/file.svg";
	import flaskConical from "../../vendor/mewa-icons/icons/flask-conical.svg";
	import foldVertical from "../../vendor/mewa-icons/icons/fold-vertical.svg";
	import folderOpen from "../../vendor/mewa-icons/icons/folder-open.svg";
	import folder from "../../vendor/mewa-icons/icons/folder.svg";
	import gauge from "../../vendor/mewa-icons/icons/gauge.svg";
	import gitBranch from "../../vendor/mewa-icons/icons/git-branch.svg";
	import gitFork from "../../vendor/mewa-icons/icons/git-fork.svg";
	import globe from "../../vendor/mewa-icons/icons/globe.svg";
	import house from "../../vendor/mewa-icons/icons/house.svg";
	import image from "../../vendor/mewa-icons/icons/image.svg";
	import info from "../../vendor/mewa-icons/icons/info.svg";
	import keyRound from "../../vendor/mewa-icons/icons/key-round.svg";
	import layers from "../../vendor/mewa-icons/icons/layers.svg";
	import lightbulb from "../../vendor/mewa-icons/icons/lightbulb.svg";
	import link from "../../vendor/mewa-icons/icons/link.svg";
	import listChecks from "../../vendor/mewa-icons/icons/list-checks.svg";
	import loaderCircle from "../../vendor/mewa-icons/icons/loader-circle.svg";
	import lock from "../../vendor/mewa-icons/icons/lock.svg";
	import logIn from "../../vendor/mewa-icons/icons/log-in.svg";
	import logOut from "../../vendor/mewa-icons/icons/log-out.svg";
	import messageCircleQuestion from "../../vendor/mewa-icons/icons/message-circle-question-mark.svg";
	import messageSquarePlus from "../../vendor/mewa-icons/icons/message-square-plus.svg";
	import messageSquare from "../../vendor/mewa-icons/icons/message-square.svg";
	import octagonAlert from "../../vendor/mewa-icons/icons/octagon-alert.svg";
	import paperclip from "../../vendor/mewa-icons/icons/paperclip.svg";
	import pencil from "../../vendor/mewa-icons/icons/pencil.svg";
	import pilcrow from "../../vendor/mewa-icons/icons/pilcrow.svg";
	import plus from "../../vendor/mewa-icons/icons/plus.svg";
	import puzzle from "../../vendor/mewa-icons/icons/puzzle.svg";
	import refreshCw from "../../vendor/mewa-icons/icons/refresh-cw.svg";
	import rocket from "../../vendor/mewa-icons/icons/rocket.svg";
	import rotateCcw from "../../vendor/mewa-icons/icons/rotate-ccw.svg";
	import rotateCw from "../../vendor/mewa-icons/icons/rotate-cw.svg";
	import save from "../../vendor/mewa-icons/icons/save.svg";
	import search from "../../vendor/mewa-icons/icons/search.svg";
	import settings2 from "../../vendor/mewa-icons/icons/settings-2.svg";
	import settings from "../../vendor/mewa-icons/icons/settings.svg";
	import skipForward from "../../vendor/mewa-icons/icons/skip-forward.svg";
	import sparkles from "../../vendor/mewa-icons/icons/sparkles.svg";
	import square from "../../vendor/mewa-icons/icons/square.svg";
	import target from "../../vendor/mewa-icons/icons/target.svg";
	import trash2 from "../../vendor/mewa-icons/icons/trash-2.svg";
	import triangleAlert from "../../vendor/mewa-icons/icons/triangle-alert.svg";
	import typeIcon from "../../vendor/mewa-icons/icons/type.svg";
	import wrench from "../../vendor/mewa-icons/icons/wrench.svg";
	import x from "../../vendor/mewa-icons/icons/x.svg";

	const icons: Record<IconName, string> = {
		"app-window": appWindow, archive, "archive-restore": archiveRestore,
		"arrow-down": arrowDown, "arrow-up": arrowUp, "book-open": bookOpen, bot, boxes, brain,
		"brain-circuit": brainCircuit, camera, check, "chevron-down": chevronDown,
		"chevron-left": chevronLeft, "chevron-right": chevronRight, "chevron-up": chevronUp,
		circle, "circle-alert": circleAlert, "circle-dot": circleDot, clock,
		"clock-arrow-left": clockArrowLeft, "code-xml": codeXml, copy,
		"corner-up-right": cornerUpRight, ellipsis, "external-link": externalLink, eye,
		"eye-off": eyeOff, file, "file-diff": fileDiff, "file-plus": filePlus,
		"file-text": fileText, "flask-conical": flaskConical, "fold-vertical": foldVertical,
		folder, "folder-open": folderOpen, gauge, "git-branch": gitBranch, "git-fork": gitFork,
		globe, house, image, info, "key-round": keyRound, layers, lightbulb, link,
		"list-checks": listChecks, "loader-circle": loaderCircle, lock, "log-in": logIn,
		"log-out": logOut, "message-circle-question": messageCircleQuestion,
		"message-square": messageSquare, "message-square-plus": messageSquarePlus,
		"octagon-alert": octagonAlert, paperclip, pencil, pilcrow, plus, puzzle,
		"refresh-cw": refreshCw, rocket, "rotate-ccw": rotateCcw, "rotate-cw": rotateCw,
		save, search, settings, "settings-2": settings2, "skip-forward": skipForward,
		sparkles, square, target, "trash-2": trash2, "triangle-alert": triangleAlert,
		type: typeIcon, wrench, x,
	};

	interface Props { name: IconName; size?: number | string; label?: string; class?: string }
	let { name, size = 16, label, class: className = "" }: Props = $props();
	let dimension = $derived(typeof size === "number" ? `${size}px` : size);
</script>

<span
	class={`mewa-icon ${className}`}
	style={`--icon-source: url("${icons[name]}"); --icon-size: ${dimension}`}
	role={label ? "img" : undefined}
	aria-label={label}
	aria-hidden={label ? undefined : "true"}
></span>

<style>
	.mewa-icon { display: inline-block; width: var(--icon-size); height: var(--icon-size); flex: 0 0 auto; background-color: currentColor; mask: var(--icon-source) center / contain no-repeat; -webkit-mask: var(--icon-source) center / contain no-repeat; }
</style>
