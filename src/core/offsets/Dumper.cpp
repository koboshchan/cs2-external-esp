#include "Dumper.hpp"

#include "core/engine/Engine.hpp"
#include "updater/http/HttpHelper.hpp"

bool Dumper::Init() {
    return GetInstance().InitImpl();
}

bool Dumper::FetchOffsetsFromCS2Dumper() {
    return GetInstance().FetchOffsetsFromCS2DumperImpl();
}

bool Dumper::InitImpl() {
    auto process = Engine::GetProcess();
    auto client = Engine::GetClient();
    auto engine = Engine::GetEngine();

    DWORD64 temp = 0;

    // client.dll

    // View Matrix
    if (!(temp = Scan(offsets::signatures::viewMatrix, client))) {
        LOGF(FATAL, "Could not find offset for 'viewMatrix'");
        return false;
    }

    offsets::viewMatrix = temp - client.base;
    LOGF(VERBOSE, "Found 'viewMatrix' offset at 0x{:X}", offsets::viewMatrix);

    // Global Variables
    if (!(temp = Scan(offsets::signatures::globalVars, client))) {
        LOGF(FATAL, "Could not find offset for 'globalVars'");
        // return false; // Not critical
    }

    offsets::globalVars = temp - client.base;
    LOGF(VERBOSE, "Found 'globalVars' offset at 0x{:X}", offsets::globalVars);

    // Entity List
    if (!(temp = Scan(offsets::signatures::entityList, client))) {
        LOGF(FATAL, "Could not find offset for 'entityList'");
        return false;
    }

    offsets::entityList = temp - client.base;
    LOGF(VERBOSE, "Found 'entityList' offset at 0x{:X}", offsets::entityList);

    // Local Player Controller
    if (!(temp = Scan(offsets::signatures::localPlayerController, client))) {
        LOGF(FATAL, "Could not find offset for 'localPlayerController'");
        return false;
    }

    offsets::localPlayerController = temp - client.base;
    LOGF(VERBOSE, "Found 'localPlayerController' offset at 0x{:X}", offsets::localPlayerController);

    // C4
    if (!(temp = Scan(offsets::signatures::plantedC4, client))) {
        LOGF(FATAL, "Could not find offset for 'plantedC4'");
        //return false;
    }

    offsets::plantedC4 = temp - client.base;
    LOGF(VERBOSE, "Found 'plantedC4' offset at 0x{:X}", offsets::plantedC4);

    // C4 carrier pointer
    if (!(temp = Scan(offsets::signatures::weaponC4, client))) {
        LOGF(FATAL, "Could not find offset for 'weaponC4 carrier'");
        return false;
    }

    offsets::weaponC4 = temp - client.base;
    LOGF(VERBOSE, "Found 'weaponC4 carrier' offset at 0x{:X}", offsets::weaponC4);

    // View Angles, Local Player Pawn and ForceJump
    // These change every game update, so they are refreshed from cs2-dumper at
    // startup (Dumper::FetchOffsetsFromCS2Dumper). The built-in values in
    // Offsets.hpp are the fallback when the network request fails.
    LOGF(VERBOSE, "Using built-in 'viewAngles' offset: 0x{:X}", offsets::viewAngles);
    LOGF(VERBOSE, "Using built-in 'localPlayerPawn' offset: 0x{:X}", offsets::localPlayerPawn);
    LOGF(VERBOSE, "Using built-in 'forceJump' offset: 0x{:X}", offsets::forceJump);

    // engine2.dll

    // Build Number
    if (!(temp = Scan(offsets::signatures::buildNumber, engine))) {
        LOGF(FATAL, "Could not find offset for 'buildNumber'");
        return false;
    }

    offsets::buildNumber = temp - engine.base;
    LOGF(VERBOSE, "Found 'buildNumber' offset at 0x{:X}", offsets::buildNumber);

    LOGF(INFO, "Successfully dumped offsets...");

    return true;
}

DWORD64 Dumper::Scan(const std::string sig, ProcessModule module) {
    auto process = Engine::GetProcess();

    if (!process)
        return 0;

    DWORD offsets = 0;
    DWORD64 address = 0;
    std::vector<DWORD64> list;

    //list = process->FindSignature(module, sig.data());
    list = ScanMemory(sig, module.base, module.base + 0x4000000);

    if (!list.size())
        return 0;

    if (!process->read_raw(list.at(0) + 3, &offsets, sizeof(DWORD)))
        return 0;

    address = list.at(0) + offsets + 7;
    return address;
}

std::vector<WORD> Dumper::StrSigToArray(const std::string& sig) {
    std::istringstream iss(sig);
    std::vector<WORD> bytes;
    std::string byte_str;

    while (iss >> byte_str) {
        if (byte_str == "??" || byte_str == "?")
            bytes.push_back(256);
        else
            bytes.push_back(static_cast<WORD>(std::stoul(byte_str, nullptr, 16)));
    }
    return bytes;
}

void Dumper::GetNextArray(std::vector<short>& next, const std::vector<WORD>& signature)
{
    auto size = signature.size();
    for (int i = 0; i < size; i++)
        next[signature[i]] = i;
}

void Dumper::ScanBlock(byte* buffer, const std::vector<short>& next, const std::vector<WORD>& signature, DWORD64 start, DWORD size, std::vector<DWORD64>& result)
{
    auto process = Engine::GetProcess();

    if (!process->read_raw(start, buffer, size))
        return;

    int length = signature.size();

    for (int i = 0, j, k; i < size;)
    {
        j = i; k = 0;

        for (; k < length && j < size && (signature[k] == buffer[j] || signature[k] == 256); k++, j++);

        if (k == length)
            result.push_back(start + i);

        if ((i + length) >= size)
            return;

        int Num = next[buffer[i + length]];
        if (Num == -1)
            i += (length - next[256]);
        else
            i += (length - Num);
    }
}

std::vector<DWORD64> Dumper::ScanMemory(const std::string& sig, DWORD64 start, DWORD64 end, int number)
{
    std::vector<DWORD64> result;
    std::vector<short> next(260, -1);

    auto process = Engine::GetProcess();

    if (!process)
        return result;

    byte* buffer = new byte[MAX_BLOCK_SIZE];

    auto signature = StrSigToArray(sig);
    if (!signature.size())
        return result;

    GetNextArray(next, signature);

    MEMORY_BASIC_INFORMATION mbi;
    while (VirtualQueryEx(process->handle_, reinterpret_cast<LPCVOID>(start), &mbi, sizeof(mbi)) != 0)
    {
        int searches = 0;
        auto size = mbi.RegionSize;

        while (size >= MAX_BLOCK_SIZE)
        {
            if (result.size() >= number) {
                delete[] buffer;
	            return result;
            }

            ScanBlock(buffer, next, signature, start + (MAX_BLOCK_SIZE * searches), MAX_BLOCK_SIZE, result);

            size -= MAX_BLOCK_SIZE;
            searches++;
        }

        ScanBlock(buffer, next, signature, start + (MAX_BLOCK_SIZE * searches), size, result);

        start += mbi.RegionSize;

        if (result.size() >= number || end != 0 && start > end)
            break;
    }

	delete[] buffer;
	return result;
}

bool Dumper::FetchOffsetsFromCS2DumperImpl() {
    LOGF(INFO, "Attempting to fetch offsets from cs2-dumper repository...");

    constexpr const char* BASE = "https://raw.githubusercontent.com/a2x/cs2-dumper/main/output/";

    json offsets_json, client_json, buttons_json;

    auto offsets_status = HttpHelper::Get(std::string(BASE) + "offsets.json", offsets_json);
    auto client_status = HttpHelper::Get(std::string(BASE) + "client_dll.json", client_json);
    auto buttons_status = HttpHelper::Get(std::string(BASE) + "buttons.json", buttons_json);

    if (offsets_status != 200 || client_status != 200) {
        LOGF(WARNING, "Failed to fetch cs2-dumper offsets (offsets: {}, client: {})", offsets_status, client_status);
        return false;
    }

    try {
        // ---- module offsets (RVA inside client.dll) ----
        const auto& client_offsets = offsets_json["client.dll"];
        if (client_offsets.contains("dwViewAngles"))
            offsets::viewAngles = client_offsets["dwViewAngles"].get<DWORD>();
        if (client_offsets.contains("dwLocalPlayerPawn"))
            offsets::localPlayerPawn = client_offsets["dwLocalPlayerPawn"].get<DWORD>();

        // ---- jump button ----
        if (buttons_status == 200 && buttons_json.contains("client.dll") && buttons_json["client.dll"].contains("jump"))
            offsets::forceJump = buttons_json["client.dll"]["jump"].get<DWORD>();

        // ---- pawn / entity fields used by the gameplay features ----
        const auto& classes = client_json["client.dll"]["classes"];

        auto apply = [&](const char* cls, const char* field, std::ptrdiff_t& target) -> bool {
            if (!classes.contains(cls))
                return false;

            const auto& fields = classes[cls]["fields"];
            if (!fields.contains(field))
                return false;

            target = fields[field].get<std::ptrdiff_t>();
            return true;
        };

        int applied = 0;
        applied += apply("C_CSPlayerPawn", "m_iShotsFired", offsets::pawn::m_iShotsFired);
        applied += apply("C_CSPlayerPawn", "m_iIDEntIndex", offsets::pawn::m_iIDEntIndex);
        applied += apply("C_CSPlayerPawn", "m_angEyeAngles", offsets::pawn::m_angEyeAngles);
        applied += apply("C_BaseModelEntity", "m_vecViewOffset", offsets::pawn::m_vecViewOffset);
        applied += apply("C_BaseEntity", "m_fFlags", offsets::pawn::m_fFlags);
        applied += apply("C_CSPlayerPawnBase", "m_flFlashDuration", offsets::pawn::m_flFlashDuration);
        applied += apply("C_BasePlayerPawn", "m_pCameraServices", offsets::pawn::m_pCameraServices);
        applied += apply("CPlayer_CameraServices", "m_vecCsViewPunchAngle", offsets::pawn::m_vecCsViewPunchAngle);

        LOGF(INFO, "Applied {} feature offsets from cs2-dumper:", applied);
        LOGF(INFO, "  dwViewAngles:            0x{:X}", offsets::viewAngles);
        LOGF(INFO, "  jump button:             0x{:X}", offsets::forceJump);
        LOGF(INFO, "  m_iShotsFired:           0x{:X}", offsets::pawn::m_iShotsFired);
        LOGF(INFO, "  m_iIDEntIndex:           0x{:X}", offsets::pawn::m_iIDEntIndex);
        LOGF(INFO, "  m_angEyeAngles:          0x{:X}", offsets::pawn::m_angEyeAngles);
        LOGF(INFO, "  m_vecViewOffset:         0x{:X}", offsets::pawn::m_vecViewOffset);
        LOGF(INFO, "  m_fFlags:                0x{:X}", offsets::pawn::m_fFlags);
        LOGF(INFO, "  m_flFlashDuration:       0x{:X}", offsets::pawn::m_flFlashDuration);
        LOGF(INFO, "  m_pCameraServices:       0x{:X}", offsets::pawn::m_pCameraServices);
        LOGF(INFO, "  m_vecCsViewPunchAngle:   0x{:X}", offsets::pawn::m_vecCsViewPunchAngle);

        if (offsets::viewAngles == 0) {
            LOGF(WARNING, "cs2-dumper did not provide dwViewAngles, aimbot/RCS will be disabled");
        }
        if (offsets::forceJump == 0) {
            LOGF(WARNING, "cs2-dumper did not provide the jump button, bhop will be disabled");
        }

        return applied > 0;
    }
    catch (std::exception& e) {
        LOGF(WARNING, "Failed to parse cs2-dumper offsets: {}", e.what());
        LOGF(WARNING, "Using built-in fallback values");
        return false;
    }
}