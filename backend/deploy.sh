docker stop iot-gateway&&docker rm iot-gateway&&docker rmi iot-gateway:1.0

docker build -t iot-gateway:1.0 .

docker run -d \
	--name iot-gateway \
	-p 9081:9081 \
	-e TZ=Asia/Shanghai \
	-v /home/iot-gateway/log:/app/log \
	-v /home/iot-gateway/data:/app/data \
	iot-gateway:1.0
