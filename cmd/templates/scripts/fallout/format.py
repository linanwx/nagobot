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

    # Parse per-player options: XML tags <PlayerName>option text</PlayerName>
    option_map = {}
    if args.options:
        import re
        for m in re.finditer(r"<([^>]+)>(.*?)</\1>", args.options, re.DOTALL):
            name, opt = m.group(1), m.group(2).strip()
            if opt:
                option_map.setdefault(name, []).append(opt)

    # --- Build response ---
    lines = []

    # Insert check results by ID
    if args.checks:
        check_store = state.get("check_results", {})
        for cid in args.checks.split(","):
            cid = cid.strip()
            fmt = check_store.get(cid)
            if fmt:
                lines.append(fmt)
                lines.append("")

    # Insert damage results by ID
    if args.damages:
        dmg_store = state.get("damage_results", {})
        for did in args.damages.split(","):
            did = did.strip()
            fmt = dmg_store.get(did)
            if fmt:
                lines.append(fmt)
                lines.append("")

    # Insert attack results by ID
    if args.attacks:
        atk_store = state.get("attack_results", {})
        for aid in args.attacks.split(","):
            aid = aid.strip()
            fmt = atk_store.get(aid)
            if fmt:
                lines.append(fmt)
                lines.append("")

    # Turn / Chapter header
    turn = state.get("turn", 0)
    chapter = state.get("chapter", 1)
    chapter_title = state.get("chapter_title", "")
    chapter_start = state.get("chapter_start_turn", 0)
    chapter_turn = turn - chapter_start

    header = f"> 📊 Chapter {chapter} · Turn {chapter_turn} (Total {turn})"
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
        hunger = player.get("hunger", 0)
        caps = player.get("caps", 0)
        ap = player.get("ap", 0)

        id_tag = f" [<@{discord_id}>]" if discord_id else ""
        lines.append(f"> 👤 {character} ({pname}){id_tag}")
        lines.append(f"> ❤️ {hp}/{max_hp} · ☢️ {rads} · 🍖 {hunger} · 💰 {caps} · ⚡ {ap}")

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
    truncated = False
    for pname, player in players.items():
        if player.get("hp", 0) <= 0:
            continue
        character = player.get("character", pname)
        raw_opts = option_map.get(pname, option_map.get(character, []))
        opts = raw_opts[:3]
        if len(raw_opts) > 3:
            truncated = True
        if opts:
            lines.append(f"{character}, what do you want to do?")
            for i, opt in enumerate(opts, 1):
                lines.append(f"{i}. {opt}")
            lines.append(f"{len(opts) + 1}. Other (describe your action)")
            lines.append("")

    template = "\n".join(lines)

    # --- Prompt hints ---
    hints = [
        "Review the template: if turn, location, items, or other state info is wrong, fix it first (e.g. call 'turn' if you forgot, 'set location' if location changed), then call format-response again.",
        f"Replace [NARRATIVE: ...] with 5-10 sentences of scene description. This chapter's turns (currently chapter turn {chapter_turn}) form one Dan Harmon's Story Circle — pace the narrative so each turn advances through the circle.",
        "Options must only describe actions. Do NOT mention difficulty values, skill names, SPECIAL attributes, success rates, or consequences in option text. No hints like '[Easy]', '[Lockpick]', or '[STR check]'.",
        "Respond in the player's language. If the player writes in Chinese, translate ALL content (narrative, options, status labels) into Chinese.",
    ]

    if truncated:
        hints.append("Options were truncated to 3 per player. Do NOT output more than 3 options per player.")

    # Dynamic hint: consecutive check failures
    check_hist = state.get("check_history", [])
    if len(check_hist) >= 3 and not any(h["passed"] for h in check_hist[-3:]):
        hints.append(
            "Players have failed multiple checks in a row. "
            "Consider changing the current obstacle — transform it, have an NPC intervene, "
            "or open an alternative path. Avoid letting players keep attempting the same blocked objective. "
            "Review your narrative for flow and dramatic engagement."
        )

    # Output plain text: template + hints
    print(template)
    print("---")
    print("(System info, do not reveal to players)")
    print(f"Mode: {mode}")
    print("Hints:")
    for h in hints:
        print(f"- {h}")
