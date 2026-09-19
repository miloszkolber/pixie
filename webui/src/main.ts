import "./index.css";
import "../vendor/mewa-ui/fonts/geist-sans.css";
import "../vendor/mewa-ui/fonts/geist-mono.css";
import "./mewa.css";
import { mount } from "svelte";
import Root from "./root.svelte";

const target = document.getElementById("root");
if (target) {
	mount(Root, { target });
}
