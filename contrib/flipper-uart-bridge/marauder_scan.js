let serial = require("serial");

let MAX_AP_DETAILS = 11;
let MAX_RESPONSE_READS = 12;
let MAX_RESPONSE_CHARS = 4096;
let responseWasTruncated = false;
let listHasMoreAPs = false;

function isWhitespace(character) {
    return character === " " || character === "\t" || character === "\r" || character === "\n";
}

function trimLine(line) {
    let start = 0;
    let end = line.length;
    while (start < end && isWhitespace(line.slice(start, start + 1))) start++;
    while (end > start && isWhitespace(line.slice(end - 1, end))) end--;
    return line.slice(start, end);
}

function readResponse() {
    let response = "";
    let readCount = 0;
    let quietReads = 0;
    let receivedData = false;
    let responseSettled = false;
    responseWasTruncated = false;

    while (readCount < MAX_RESPONSE_READS) {
        readCount++;
        let chunk = serial.readAny(200);
        if (chunk === undefined || chunk.length === 0) {
            quietReads++;
            if (receivedData && quietReads >= 2) {
                responseSettled = true;
                break;
            }
            continue;
        }

        receivedData = true;
        quietReads = 0;
        let remaining = MAX_RESPONSE_CHARS - response.length;
        if (chunk.length > remaining) {
            response += chunk.slice(0, remaining);
            responseWasTruncated = true;
            break;
        }
        response += chunk;
        if (response.length >= MAX_RESPONSE_CHARS) {
            responseWasTruncated = true;
            break;
        }
    }

    if (receivedData && !responseSettled) responseWasTruncated = true;
    return response;
}

function parseDecimal(value) {
    if (value.length === 0 || value.length > 10) return -1;
    let number = 0;
    for (let i = 0; i < value.length; i++) {
        let digit = "0123456789".indexOf(value.slice(i, i + 1));
        if (digit < 0) return -1;
        number = number * 10 + digit;
        if (number > 2147483647) return -1;
    }
    return number;
}

function parseAPRowIndex(line) {
    if (line.slice(0, 1) !== "[") return -1;

    let indexEnd = line.indexOf("]");
    if (indexEnd < 2) return -2;
    let index = parseDecimal(line.slice(1, indexEnd));
    if (index < 0) return -2;

    let channelStart = indexEnd + 1;
    if (line.slice(channelStart, channelStart + 1) !== "[") return -2;
    let channelEnd = line.indexOf("]", channelStart + 1);
    if (channelEnd < 0) return -2;

    let channel = line.slice(channelStart + 1, channelEnd);
    if (channel.slice(0, 3) !== "CH:" && channel.slice(0, 8) !== "CHANNEL:") return -2;
    let channelValue = channel.slice(
        channel.slice(0, 3) === "CH:" ? 3 : 8,
        channel.length);
    if (parseDecimal(channelValue) < 0) return -2;
    if (trimLine(line.slice(channelEnd + 1, line.length)).length === 0) return -2;
    return index;
}

function isSelectionSummary(line) {
    let suffix = " selected";
    if (line.length <= suffix.length) return false;
    if (line.slice(line.length - suffix.length, line.length) !== suffix) return false;
    return parseDecimal(line.slice(0, line.length - suffix.length)) >= 0;
}

function parseAPList(response, indices) {
    let sawAPRow = false;
    let sawSelectionSummary = false;
    let lineStart = 0;
    listHasMoreAPs = false;

    while (lineStart <= response.length) {
        let lineEnd = response.indexOf("\n", lineStart);
        if (lineEnd < 0) lineEnd = response.length;
        let line = trimLine(response.slice(lineStart, lineEnd));
        lineStart = lineEnd + 1;

        if (line.length === 0 || line === ">" || line === "#list -a" || line === "list -a") {
            if (lineStart > response.length) break;
            continue;
        }
        if (isSelectionSummary(line)) {
            sawSelectionSummary = true;
            if (lineStart > response.length) break;
            continue;
        }

        let index = parseAPRowIndex(line);
        if (index < 0) return false;
        sawAPRow = true;
        let duplicate = false;
        for (let i = 0; i < indices.length; i++) {
            if (indices[i] === index) duplicate = true;
        }
        if (duplicate) return false;
        if (indices.length < MAX_AP_DETAILS) {
            indices.splice(indices.length, 0, index);
        } else {
            listHasMoreAPs = true;
        }
        if (lineStart > response.length) break;
    }

    return sawAPRow || sawSelectionSummary;
}

function fail(message) {
    print("ERROR: " + message);
    serial.end();
}

function runScan() {
    serial.setup("usart", 115200);
    delay(1000);
    serial.readAny(500);
    serial.write("\r\n");
    delay(500);
    serial.readAny(500);

    print("Stopping any prior Marauder operation");
    serial.write("stopscan\r\n");
    readResponse();
    if (responseWasTruncated) {
        fail("preflight stopscan response exceeded the buffer limit");
        return;
    }

    print("Starting a fresh five-second AP scan");
    serial.write("scanall\r\n");
    for (let i = 0; i < 10; i++) {
        delay(500);
        serial.readAny(50);
    }

    serial.write("stopscan\r\n");
    readResponse();
    if (responseWasTruncated) {
        fail("stopscan response exceeded the buffer limit");
        return;
    }

    serial.write("list -a\r\n");
    let listResponse = readResponse();
    print(listResponse);
    if (responseWasTruncated) {
        fail("list -a response exceeded the buffer limit");
        return;
    }

    let indices = [];
    if (!parseAPList(listResponse, indices)) {
        fail("malformed list -a response");
        return;
    }
    if (indices.length === 0) {
        print("No APs found; no details queried.");
        serial.end();
        return;
    }
    if (listHasMoreAPs) print("Details limited to the first 11 listed APs.");

    print("Reading details for AP indexes reported by list -a");
    for (let i = 0; i < indices.length; i++) {
        let command = "info -a " + indices[i] + "\r\n";
        serial.write(command);
        let detail = readResponse();
        if (responseWasTruncated) {
            fail("info -a response exceeded the buffer limit");
            return;
        }
        if (detail.length === 0) {
            fail("info -a returned no response for a listed AP");
            return;
        }
        print(detail);
    }

    serial.end();
}

runScan();
