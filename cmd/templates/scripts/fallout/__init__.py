"""Fallout Wasteland RPG — Game Engine Package."""

from .dice import cmd_roll, cmd_check, cmd_damage, cmd_initiative
from .player import cmd_add_player, cmd_remove_player, cmd_hurt, cmd_heal, cmd_rads, cmd_caps, cmd_ap, cmd_inventory, cmd_skill_up
from .world import cmd_init, cmd_status, cmd_set, cmd_turn
from .items import cmd_use_item, cmd_effect, cmd_rest, cmd_recover
from .events import cmd_loot, cmd_trade, cmd_npc_gen
from .enemy import cmd_enemy_add, cmd_enemy_hurt, cmd_enemy_attack

COMMANDS = {
    "init": cmd_init,
    "status": cmd_status,
    "add-player": cmd_add_player,
    "remove-player": cmd_remove_player,
    "roll": cmd_roll,
    "check": cmd_check,
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

    "turn": cmd_turn,
    "loot": cmd_loot,
    "trade": cmd_trade,
    "skill-up": cmd_skill_up,
    "npc-gen": cmd_npc_gen,
    "recover": cmd_recover,
    "enemy-add": cmd_enemy_add,
    "enemy-hurt": cmd_enemy_hurt,
    "enemy-attack": cmd_enemy_attack,
}
