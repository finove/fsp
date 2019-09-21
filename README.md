# fsp
fsp client


## fspd

fspd -f fspd.conf -p 9531 -d /tmp -P /tmp/fspd.pid

## proxy

set http_proxy=http://127.0.0.1:1080
set https_proxy=http://127.0.0.1:1080

## docker

1. 列出所有容器
docker ps -a -q

2. 停止所有容器
docker stop $(docker ps -a -q)

3. 删除所有容器
docker rm $(docker ps -a -q)

4. 查看镜像
docker images

5. 删除指定id的镜像
docker rmi <image id>

6. 删除全部镜像
docker rmi $(docker images -q)

7. 启动容器
docker run -it -p 9531:9531/udp centos /bin/bash
docker start 6b407fb8d0b2

8. 进入容器
docker exec -it 6b407fb8d0b2 /bin/bash
docker attach 6b407fb8d0b2

9. 复制文件到容器
docker cp fspd.conf 6b407fb8d0b2:/tmp/