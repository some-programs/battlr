"use strict";

const submitVote = (battleName, entryID, score) => {
  const payload = {
    battle_name: battleName,
    entry_id: entryID,
    score: score,
  };
  fetch("/api/vote/", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
};

const onVoteFor = (event) => {
  event.preventDefault();
  const el = event.srcElement;
  const score = Number.parseInt(el.attributes.score.value);
  const entry = el.attributes.entry.value;
  const battle = el.attributes.battle.value;

  const elementsForEntry = document.querySelectorAll(
    `button.vote[entry="${CSS.escape(entry)}"]`,
  );
  for (const e of elementsForEntry) {
    e.classList.remove("vote-yes");
  }

  const elementsForScore = document.querySelectorAll(
    `button.vote[score="${CSS.escape(score)}"]`,
  );
  for (const e of elementsForScore) {
    e.classList.remove("vote-yes");
  }
  el.classList.add("vote-yes");

  setTimeout(() => {
    submitVote(battle, entry, score);
  }, 0);
};

for (const el of document.querySelectorAll("button.vote")) {
  el.addEventListener("click", onVoteFor);
}

const submitUnvote = (battleName) => {
  const payload = {
    battle_name: battleName,
  };
  fetch("/api/unvote/", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
};

const onUnvote = (event) => {
  event.preventDefault();
  const el = event.srcElement;
  const battle = el.attributes.battle.value;

  for (const e of document.querySelectorAll("button.vote")) {
    e.classList.remove("vote-yes");
  }

  setTimeout(() => {
    submitUnvote(battle);
  }, 0);
};

document.querySelector("button.unvote").addEventListener("click", onUnvote);

const toggleNotes = (event) => {
  const notesElements = Array.from(document.querySelectorAll(".notes"));
  for (const el of notesElements) {
    el.classList.toggle("hidden");
  }
};
document.getElementById("toggle-notes").addEventListener("input", toggleNotes);
