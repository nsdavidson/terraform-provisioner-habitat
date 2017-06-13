#!/usr/bin/env bash
TFVAR_PATH=$1
CURRENT_DIR=`pwd`
SUCCESSFUL_EXAMPLES=(redis)
UNSUCCESSFUL_EXAMPLES=(validation)

for e in "${SUCCESSFUL_EXAMPLES[@]}"; do
  cd $CURRENT_DIR
  echo "TESTING $e"
  echo "Running 'terraform plan'..."
  cd ../test/examples/successful/$e
  terraform plan -var-file=$TFVAR_PATH
  if [ $? -eq 1 ]; then
    echo "terraform plan failed"
    exit 1
  else
    echo "terraform plan succeeded"
  fi

  terraform apply -var-file=$TFVAR_PATH
  if [ $? -eq 1 ]; then
    echo "terraform apply failed"
    exit 1
  else
    echo "terraform apply succeeded"
  fi

  IP_LIST=`terraform output ips`
  USER_NAME=`terraform output username`
  KEY_PATH=`terraform output key_path`
  for i in $(echo $IP_LIST | tr ",", "\n")
  do
    echo "RUNNING inspec exec ./inspec/example.rb -t ssh://$USER_NAME@$i -i $KEY_PATH"
    inspec exec ./inspec/example.rb -t ssh://$USER_NAME@$i -i $KEY_PATH
    if [ $? -eq 1 ]; then
      echo "InSpec failed"
      exit 1
    else
      echo "InSpec passed"
    fi
  done
  
  terraform destroy -force -var-file=$TFVAR_PATH
done

for e in "${UNSUCCESSFUL_EXAMPLES[@]}"; do
  cd $CURRENT_DIR
  echo "TESTING $e"
  echo "Running 'terraform plan'..."
  cd ../example/$e
  terraform plan
  if [ $? -eq 1 ]; then
    echo "terraform plan failed as expected"
  else
    echo "terraform plan succeeded"
    exit 1
  fi
done