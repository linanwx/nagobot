"""Fallout Wasteland RPG — Game Engine Package."""

from .dice import cmd_roll, cmd_check, cmd_assist_check, cmd_oracle, cmd_damage, cmd_initiative
from .player import cmd_add_player, cmd_remove_player, cmd_hurt, cmd_heal, cmd_rads, cmd_caps, cmd_ap, cmd_inventory, cmd_skill_up
from .world import cmd_init, cmd_status, cmd_set, cmd_flag, cmd_turn, cmd_log
from .items import cmd_use_item, cmd_effect, cmd_rest, cmd_recover
from .events import cmd_event, cmd_loot, cmd_trade, cmd_npc_gen, cmd_weather, cmd_help

COMMANDS = {
    "init": cmd_init,
    "status": cmd_status,
    "add-player": cmd_add_player,
    "remove-player": cmd_remove_player,
    "roll": cmd_roll,
    "check": cmd_check,
    "assist-check": cmd_assist_check,
    "oracle": cmd_oracle,
    "damage": cmd_damage,
    "initiative": cmd_initiative,
    "hurt": cmd_hurt,
    "heal": cmd_heal,
    "rads": cmd_rads,
    "caps": cmd_caps,
    "ap": cmd_ap,
    "inventory": cmd_inventory,
    "use-item": cmd_use_item,
    "effect": cmd_effect,
    "rest": cmd_rest,
    "set": cmd_set,
    "flag": cmd_flag,
    "turn": cmd_turn,
    "log": cmd_log,
    "event": cmd_event,
    "loot": cmd_loot,
    "trade": cmd_trade,
    "skill-up": cmd_skill_up,
    "npc-gen": cmd_npc_gen,
    "weather": cmd_weather,
    "recover": cmd_recover,
    "help": cmd_help,
}
