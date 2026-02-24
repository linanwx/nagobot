"""Format response: auto-generate status panel + response template."""

import json
from .util import load_state, error, output, get_mode, get_effective_special


def cmd_format_response(args):
    """Generate a formatted response template from current game state."""
    state = load_state()
    if not state:
        return error("Game not initialized. Run 'init' first.",
                      hint="Call 'init' to create a new game, then 'add-player' to add players.")

    summary = args.summary or ""
    players = state.get("players", {})
    mode = get_mode(state)

    # Parse per-player option counts: "PlayerA:3,PlayerB:3"
    option_map = {}
    if args.options:
        for pair in args.options.split(","):
            pair = pair.strip()
            if ":" in pair:
                name, count = pair.rsplit(":", 1)
                try:
                    option_map[name.strip()] = int(count.strip())
                except ValueError:
                    option_map[name.strip()] = 3
            else:
                option_map[pair.strip()] = 3

    # --- Build status panel ---
    lines = []

    # Turn / Chapter header
    turn = state.get("turn", 0)
    chapter = state.get("chapter", 1)
    chapter_title = state.get("chapter_title", "")
    chapter_start = state.get("chapter_start_turn", 0)
    chapter_turn = turn - chapter_start

    header = f"> 📊 Turn {turn} (Chapter Turn {chapter_turn}) · Chapter {chapter}"
    if chapter_title:
        header += f": {chapter_title}"
    if mode == "combat":
        combat_round = state.get("combat_round", 0)
        header += f" · ⚔️ Combat Round {combat_round}"
    lines.append(header)

    # Time / Weather / Location
    time_of_day = state.get("time_of_day", "Unknown")
    weather = state.get("weather", "Unknown")
    location = state.get("location", "Unknown")
    lines.append(f"> 🕐 {time_of_day} · {weather} · {location}")

    # Quest
    quest = state.get("quest", "")
    if quest:
        lines.append(f"> 🎯 {quest}")

    lines.append("")

    # Player status bars
    for pname, player in players.items():
        character = player.get("character", pname)
        discord_id = player.get("id", "")
        hp = player.get("hp", 0)
        max_hp = player.get("max_hp", 0)
        rads = player.get("rads", 0)
        caps = player.get("caps", 0)
        ap = player.get("ap", 0)

        id_tag = f" [<@{discord_id}>]" if discord_id else ""
        lines.append(f"> 👤 {character} ({pname}){id_tag}")
        lines.append(f"> ❤️ {hp}/{max_hp} · ☢️ {rads} · 💰 {caps} · ⚡ {ap}")

        # Inventory
        inventory = player.get("inventory", [])
        if inventory:
            items = []
            for item in inventory:
                if isinstance(item, dict):
                    qty = item.get("qty", 1)
                    name = item.get("name", "?")
                    items.append(f"{name}×{qty}" if qty > 1 else name)
                else:
                    items.append(str(item))
            lines.append(f"> 🎒 {', '.join(items)}")
        else:
            lines.append("> 🎒 (empty)")

        # Active effects
        effects = player.get("effects", [])
        if effects:
            effect_strs = []
            for e in effects:
                if isinstance(e, dict):
                    ename = e.get("name", "?")
                    dur = e.get("duration")
                    effect_strs.append(f"{ename}({dur}t)" if dur else ename)
                else:
                    effect_strs.append(str(e))
            lines.append(f"> 💊 {', '.join(effect_strs)}")

        lines.append("")

    # Enemies (in combat)
    enemies = state.get("enemies", {})
    alive_enemies = {k: v for k, v in enemies.items() if v.get("status") == "alive"}
    if alive_enemies:
        enemy_strs = []
        for ename, enemy in alive_enemies.items():
            ehp = enemy.get("hp", 0)
            emax = enemy.get("max_hp", 0)
            enemy_strs.append(f"{ename} ❤️{ehp}/{emax}")
        lines.append(f"> ⚔️ Enemies: {' | '.join(enemy_strs)}")
        lines.append("")

    # --- Narrative placeholder ---
    lines.append(f"[NARRATIVE: {summary}]")
    lines.append("")

    # --- Options per player ---
    for pname, player in players.items():
        if player.get("hp", 0) <= 0:
            continue
        character = player.get("character", pname)
        count = option_map.get(pname, option_map.get(character, 3))
        lines.append(f"{character}, what do you want to do?")
        for i in range(1, count + 1):
            lines.append(f"{i}. [option {i}]")
        lines.append("")

    template = "\n".join(lines)

    # --- Prompt hints ---
    hints = [
        "Replace [NARRATIVE: ...] with 5-10 sentences of scene description.",
        "Replace each [option N] with a concrete action. Do NOT mention difficulty, skill names, or consequences in options.",
    ]

    # Tag skill reminders per player
    for pname, player in players.items():
        if player.get("hp", 0) <= 0:
            continue
        tags = player.get("tag_skills", [])
        if tags:
            character = player.get("character", pname)
            hints.append(f"{character}'s tag skills: {', '.join(tags)} — design at least one option that uses them.")

    output({
        "ok": True,
        "template": template,
        "hints": hints,
        "mode": mode,
    })
