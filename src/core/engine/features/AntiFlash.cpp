#include "AntiFlash.hpp"
#include "common.hpp"
#include "core/engine/Engine.hpp"
#include "core/offsets/Offsets.hpp"

void AntiFlash::Run() {
    if (!cfg::antiflash::enabled)
        return;
    
    auto cache = Cache::CopySnapshot();
    
    Player* local_player = nullptr;
    for (auto& player : cache.players) {
        if (player.localplayer) {
            local_player = &player;
            break;
        }
    }

    if (!local_player || !local_player->alive)
        return;
    
    auto p = Engine::GetProcess();
    if (!p)
        return;
    
    // Offsets are refreshed from cs2-dumper at startup when available
    const std::ptrdiff_t flash_offsets[] = {
        offsets::pawn::m_flFlashDuration,
        offsets::pawn::m_flFlashOverlayAlpha
    };

    float zero = 0.0f;
    for (auto offset : flash_offsets) {
        p->write<float>(local_player->GetPawnAddress() + offset, zero);
    }
}
