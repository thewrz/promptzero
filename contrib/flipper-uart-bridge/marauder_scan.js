
let serial = require("serial");
serial.setup("usart", 115200);
delay(1000);
serial.readAny(500);
serial.write("\r\n");
delay(500);
serial.readAny(500);
print("Starting a fresh five-second AP scan");
serial.write("scanall\r\n");
for (let i = 0; i < 10; i++) {
    delay(500);
    serial.readAny(50);
}
serial.write("stopscan\r\n");
for (let i = 0; i < 4; i++) {
    delay(100);
    serial.readAny(100);
}
serial.write("list -a\r\n");
for (let i = 0; i < 4; i++) {
    delay(100);
    let data = serial.readAny(100);
    if (data !== undefined) {
        print(data);
    }
}
print("Raw details are limited to AP indexes 0 through 10");
let cmds = ["info -a 0\r\n","info -a 1\r\n","info -a 2\r\n","info -a 3\r\n","info -a 4\r\n","info -a 5\r\n","info -a 6\r\n","info -a 7\r\n","info -a 8\r\n","info -a 9\r\n","info -a 10\r\n"];
for (let i = 0; i < cmds.length; i++) {
    serial.write(cmds[i]);
    delay(300);
    for (let j = 0; j < 6; j++) {
        delay(200);
        let data = serial.readAny(200);
        if (data !== undefined) {
            print(data);
        }
    }
}
serial.end();
