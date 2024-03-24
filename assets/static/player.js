"use strict";

const isAutoplayEnabled = () => {
  return document.querySelector("#autoplay").checked;
};

const getPlayers = () => {
  return Array.from(document.querySelectorAll("audio"));
};

const getPlayer = (idx) => {
  return getPlayers()[idx];
};

const startPlayer = (idx) => {
  getPlayer(idx).play();
};

const audioElementIsPlaying = (el) => {
  return el.currentTime > 0 && !el.paused && !el.ended && el.readyState > 2;
};

const isAnyPlaying = () => {
  for (const p of getPlayers()) {
    if (audioElementIsPlaying(p)) {
      return true;
    }
  }
  return false;
};

const updateDelayValue = (event) => {
  event.preventDefault();
  const sliderEl = document.getElementById("delay");
  const valueEl = document.getElementById("delay-value");
  valueEl.textContent = sliderEl.value;
};

document.getElementById("delay").addEventListener("input", updateDelayValue);

const onPlay = (event) => {
  getPlayers().map((el) => {
    if (el === event.srcElement) {
      document
        .querySelector(`.entry[idx="${el.attributes.idx.value}"]`)
        .classList.add("entry-playing");
      return;
    }
    document
      .querySelector(`.entry[idx="${el.attributes.idx.value}"]`)
      .classList.remove("entry-playing");
    el.pause();
  });
};

const onEnded = (event) => {
  if (!isAutoplayEnabled()) {
    return;
  }
  const players = getPlayers();
  const currentIdx = players.indexOf(event.srcElement);
  const delay =
    100 + Number.parseInt(document.querySelector("#delay").value) * 1000;
  if (currentIdx + 1 < players.length) {
    getPlayer(currentIdx + 1).load();
    setTimeout(() => {
      if (!isAutoplayEnabled()) {
        return;
      }
      if (!isAnyPlaying()) {
        startPlayer(currentIdx + 1);
      }
    }, delay);
  }
};

Array.from(document.querySelectorAll("audio")).map((el) => {
  el.addEventListener("play", onPlay);
  el.addEventListener("ended", onEnded);
});
