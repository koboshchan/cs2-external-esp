#pragma once
#include "core/engine/classes/Bones.hpp"
#include "core/engine/classes/Weapon.hpp"
#include "core/engine/classes/ObserverServices.hpp"

class Player {
public:
    Player() {}
    Player(int index, uintptr_t el, uintptr_t le) 
        : index(index), entity_list(el), list_entry(le){}

    bool Update();
    bool GetBounds(view_matrix_t matrix, Vec2_t size, std::pair<Vec2_t, Vec2_t>& bounds);

    // Raw pawn address, used by the gameplay features (triggerbot, bhop, anti-flash)
    uintptr_t GetPawnAddress() const { return pawn; }
public:
    int8_t index = -1; // To use as invalid/un-initialize check

    Vec3_t pos;
    Vec3_t vel;

    // Gameplay feature state
    Vec3_t aim_punch;
    Vec2_t eye_angles;  // Current view angles (pitch, yaw)
    Vec3_t view_offset; // View height offset (changes when crouching)
    int shots_fired = 0;
    float fov = 90.f;   // Per-player horizontal FOV (scoped weapons change it)

    int ping = 0;
    int team = 0;
    int health = 0;
    int armor = 0;
    int money = 0;

    bool bot = true;
    bool alive = false;
    bool scoped = false;
    bool flashed = false;
    bool spotted = false;
    bool defusing = false;
    bool localplayer = false;
    bool has_c4 = false;

    char name[32];
    //std::string name;
    uint64_t steam_id{};

    Weapon weapon;
    int32_t ammo;
    bool is_reloading;

    std::vector<bone_pos> bone_list;

    int pawn_controller_addr;
    ObserverServices observer_services;
private:
    uintptr_t list_entry;
    uintptr_t entity_list;

    uintptr_t pawn;
    uintptr_t controller;
    
    bone_data bones[30]{};
private:
    bool GetPawn();
    bool GetController();

    bool UpdatePawn();
    bool UpdateWeapon();
    bool UpdateSkeleton();
    bool UpdateController();
    bool UpdateObserverServices();
};